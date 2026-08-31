package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/ledger"
)

const (
	maxEventBytes = 8 << 20
	maxEventLine  = 2 << 20
	cleanupWait   = 2 * time.Second
)

type sessionPurpose string

const (
	purposeAdmission sessionPurpose = "admission"
	purposeExecution sessionPurpose = "execution"
)

// Runtime executes one specific OpenCode provider/model binding. Every call
// creates a fresh session; it never resumes a prior Veto or user session.
type Runtime struct {
	config    Config
	discovery Discovery
	model     Model
	deps      Dependencies
}

var _ execution.RuntimeAdapter = (*Runtime)(nil)
var _ execution.EventTaskExecutor = (*Runtime)(nil)

// NewRuntime binds one discovered provider/model to an OpenCode transport.
func NewRuntime(config Config, discovery Discovery, model Model, deps Dependencies) *Runtime {
	return &Runtime{config: config, discovery: discovery, model: model, deps: fillDependencies(deps)}
}

func (r *Runtime) RuntimeID() string { return "opencode" }

// EffectiveTools is deliberately unknown until OpenCode exposes the tools for
// the active project/session. Veto must not infer shell, file, or browser access
// from a model name.
func (r *Runtime) EffectiveTools() []string { return nil }

func (r *Runtime) EffectiveToolsKnown() bool { return false }

func (r *Runtime) Run(ctx context.Context, prompt string) execution.Result {
	return r.execute(ctx, purposeAdmission, prompt, execution.ExecutionOptions{}, io.Discard, nil)
}

func (r *Runtime) Execute(ctx context.Context, prompt string, options execution.ExecutionOptions) execution.Result {
	return r.execute(ctx, purposeExecution, prompt, options, io.Discard, nil)
}

func (r *Runtime) ExecuteWithEvents(
	ctx context.Context,
	prompt string,
	options execution.ExecutionOptions,
	w io.Writer,
	emit func(execution.RuntimeEvent),
) execution.Result {
	if w == nil {
		w = io.Discard
	}
	return r.execute(ctx, purposeExecution, prompt, options, w, emit)
}

func (r *Runtime) execute(
	ctx context.Context,
	purpose sessionPurpose,
	prompt string,
	options execution.ExecutionOptions,
	w io.Writer,
	emit func(execution.RuntimeEvent),
) execution.Result {
	if !validIdentifier(r.model.Provider) || !validIdentifier(r.model.ID) {
		return execution.Result{Error: errors.New("invalid OpenCode provider/model binding")}
	}
	switch r.config.Mode {
	case ModeCLI:
		return r.executeCLI(ctx, purpose, prompt, options, w, emit)
	case ModeAttach, ModeManaged:
		return r.executeServer(ctx, purpose, prompt, options, w, emit)
	default:
		return execution.Result{Error: fmt.Errorf("unsupported OpenCode mode %q", r.config.Mode)}
	}
}

func (r *Runtime) executeServer(
	ctx context.Context,
	purpose sessionPurpose,
	prompt string,
	_ execution.ExecutionOptions,
	w io.Writer,
	emit func(execution.RuntimeEvent),
) (result execution.Result) {
	server, err := ValidateServerURL(r.config.Server)
	if r.config.Mode == ModeManaged && strings.TrimSpace(r.config.Server) == "" {
		server = "http://127.0.0.1:4096"
		err = nil
	}
	if err != nil {
		result.Error = err
		return result
	}

	var connection *Connection
	authUser := r.deps.Getenv("OPENCODE_SERVER_USERNAME")
	authValue := r.deps.Getenv("OPENCODE_SERVER_PASSWORD")
	if r.config.Mode == ModeManaged {
		connection, err = Connect(ctx, r.config, r.deps)
		if err != nil {
			result.Error = err
			return result
		}
		server = connection.Discovery.Server
		authUser, authValue = connection.authPair()
		defer func() {
			if closeErr := connection.Close(); closeErr != nil && result.Error == nil {
				result.Error = fmt.Errorf("stop managed OpenCode server: %w", closeErr)
			}
		}()
	}

	title, err := internalSessionTitle(purpose)
	if err != nil {
		result.Error = err
		return result
	}
	create := map[string]any{
		"title":    title,
		"model":    map[string]string{"providerID": r.model.Provider, "id": r.model.ID},
		"metadata": map[string]any{"veto_internal": true, "veto_purpose": string(purpose)},
	}
	if purpose == purposeAdmission {
		create["permission"] = []map[string]string{{"permission": "*", "pattern": "*", "action": "deny"}}
	}
	body, err := json.Marshal(create)
	if err != nil {
		result.Error = err
		return result
	}
	body, status, err := r.request(ctx, http.MethodPost, server+"/session", authUser, authValue, body)
	if err != nil {
		result.Error = fmt.Errorf("create OpenCode session: %w", err)
		return result
	}
	if status != http.StatusOK && status != http.StatusCreated {
		result.Error = fmt.Errorf("create OpenCode session: HTTP %d", status)
		return result
	}
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &created) != nil || !validSessionID(created.ID) {
		result.Error = errors.New("create OpenCode session: response does not contain a valid session id")
		return result
	}

	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	events := make(chan wireEvent, 32)
	ready := make(chan error, 1)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- r.readEvents(streamCtx, server+"/global/event", authUser, authValue, ready, events)
		close(events)
	}()
	select {
	case readyErr := <-ready:
		if readyErr != nil {
			result.Error = fmt.Errorf("subscribe to OpenCode events: %w", readyErr)
			return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
		}
	case <-ctx.Done():
		result.Error = ctx.Err()
		return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
	}

	promptBody, err := json.Marshal(map[string]any{
		"model": map[string]string{"providerID": r.model.Provider, "modelID": r.model.ID},
		"parts": []map[string]string{{"type": "text", "text": prompt}},
	})
	if err != nil {
		result.Error = err
		return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
	}
	_, status, err = r.request(ctx, http.MethodPost, server+"/session/"+created.ID+"/prompt_async", authUser, authValue, promptBody)
	if err != nil || (status != http.StatusNoContent && status != http.StatusOK) {
		if err == nil {
			err = fmt.Errorf("HTTP %d", status)
		}
		result.Error = fmt.Errorf("prompt OpenCode session: %w", err)
		return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
	}

	state := eventState{sessionID: created.ID, writer: w, emit: emit, textByPart: make(map[string]string), toolStates: make(map[string]string), artifacts: make(map[string]bool)}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				select {
				case streamErr := <-streamDone:
					if streamErr == nil {
						streamErr = io.ErrUnexpectedEOF
					}
					result = state.result(fmt.Errorf("OpenCode event stream ended: %w", streamErr))
				default:
					result = state.result(errors.New("OpenCode event stream ended before the session became idle"))
				}
				return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
			}
			done, processErr := state.process(ctx, r, server, authUser, authValue, event)
			if processErr != nil {
				result = state.result(processErr)
				return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
			}
			if done {
				result = state.result(state.failure)
				return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
			}
		case streamErr := <-streamDone:
			if streamErr == nil {
				streamErr = io.ErrUnexpectedEOF
			}
			result = state.result(fmt.Errorf("OpenCode event stream ended: %w", streamErr))
			return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
		case <-ctx.Done():
			result = state.result(ctx.Err())
			return r.cleanupServerSession(server, created.ID, authUser, authValue, result)
		}
	}
}

func (r *Runtime) cleanupServerSession(server, sessionID, username, password string, result execution.Result) execution.Result {
	if errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
		abortCtx, cancelAbort := context.WithTimeout(context.Background(), cleanupWait)
		_, _, _ = r.request(abortCtx, http.MethodPost, server+"/session/"+sessionID+"/abort", username, password, nil)
		cancelAbort()
	}
	deleteCtx, cancelDelete := context.WithTimeout(context.Background(), cleanupWait)
	defer cancelDelete()
	_, status, err := r.request(deleteCtx, http.MethodDelete, server+"/session/"+sessionID, username, password, nil)
	if err != nil && result.Error == nil {
		result.Error = fmt.Errorf("delete internal OpenCode session: %w", err)
	} else if err == nil && status != http.StatusOK && status != http.StatusNoContent && result.Error == nil {
		result.Error = fmt.Errorf("delete internal OpenCode session: HTTP %d", status)
	}
	return result
}

func (r *Runtime) request(ctx context.Context, method, target, username, password string, body []byte) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	setBasicAuth(request, username, password)
	response, err := r.deps.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(responseBody) > maxResponseBytes {
		return nil, response.StatusCode, errors.New("OpenCode response exceeds the size limit")
	}
	return responseBody, response.StatusCode, nil
}

type eventEnvelope struct {
	Payload wireEvent `json:"payload"`
}

type wireEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

func (r *Runtime) readEvents(ctx context.Context, target, username, password string, ready chan<- error, events chan<- wireEvent) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		ready <- err
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	setBasicAuth(request, username, password)
	response, err := r.deps.Do(request)
	if err != nil {
		ready <- err
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("HTTP %d", response.StatusCode)
		ready <- err
		return err
	}
	ready <- nil

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), maxEventLine)
	var data strings.Builder
	total := 0
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		var envelope eventEnvelope
		if !utf8.ValidString(raw) || json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Payload.Type == "" {
			return errors.New("OpenCode event stream contains an invalid event")
		}
		select {
		case events <- envelope.Payload:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxEventBytes {
			return errors.New("OpenCode event stream exceeds the size limit")
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return io.EOF
}

type eventState struct {
	sessionID  string
	writer     io.Writer
	emit       func(execution.RuntimeEvent)
	output     strings.Builder
	textByPart map[string]string
	toolStates map[string]string
	artifacts  map[string]bool
	usage      execution.Usage
	cost       float64
	costKnown  bool
	finish     string
	truncated  bool
	failure    error
}

func (s *eventState) process(ctx context.Context, runtime *Runtime, server, username, password string, event wireEvent) (bool, error) {
	var base struct {
		SessionID string `json:"sessionID"`
	}
	if len(event.Properties) > 0 && json.Unmarshal(event.Properties, &base) != nil {
		return false, errors.New("OpenCode event properties are malformed")
	}
	if base.SessionID != "" && base.SessionID != s.sessionID {
		return false, nil
	}
	switch event.Type {
	case "message.part.delta":
		return false, s.processTextDelta(event.Properties)
	case "message.part.updated":
		return false, s.processPartUpdated(event.Properties)
	case "message.updated":
		return false, s.processMessageUpdated(event.Properties)
	case "session.diff":
		var value struct {
			Diff []json.RawMessage `json:"diff"`
		}
		if json.Unmarshal(event.Properties, &value) != nil {
			return false, errors.New("OpenCode diff event is malformed")
		}
		s.artifactEvent("session-diff", "diff", len(value.Diff))
	case "permission.asked":
		return false, s.rejectPermission(ctx, runtime, server, username, password, event.Properties)
	case "permission.replied":
		var value struct {
			Reply string `json:"reply"`
		}
		if json.Unmarshal(event.Properties, &value) == nil && (value.Reply == "once" || value.Reply == "always") {
			s.emitEvent(execution.RuntimeEvent{Kind: execution.RuntimeApprovalGranted, Status: "granted"})
		}
	case "session.error":
		var value struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(event.Properties, &value) != nil {
			return false, errors.New("OpenCode session error is malformed")
		}
		s.setFailure(value.Error)
	case "session.status":
		var value struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(event.Properties, &value) != nil {
			return false, errors.New("OpenCode session status is malformed")
		}
		return value.Status.Type == "idle", nil
	case "session.idle":
		return true, nil
	}
	return false, nil
}

type wireTokens struct {
	Total  *float64 `json:"total"`
	Input  float64  `json:"input"`
	Output float64  `json:"output"`
}

type wirePart struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Text  string   `json:"text"`
	Tool  string   `json:"tool"`
	Files []string `json:"files"`
	State struct {
		Status      string            `json:"status"`
		Attachments []json.RawMessage `json:"attachments"`
	} `json:"state"`
	Reason string      `json:"reason"`
	Cost   *float64    `json:"cost"`
	Tokens *wireTokens `json:"tokens"`
}

func (s *eventState) processTextDelta(raw json.RawMessage) error {
	var value struct {
		PartID string `json:"partID"`
		Field  string `json:"field"`
		Delta  string `json:"delta"`
	}
	if json.Unmarshal(raw, &value) != nil || value.PartID == "" {
		return errors.New("OpenCode text delta is malformed")
	}
	if value.Field != "text" {
		return nil
	}
	return s.appendText(value.PartID, value.Delta)
}

func (s *eventState) processPartUpdated(raw json.RawMessage) error {
	var value struct {
		Part wirePart `json:"part"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Part.Type == "" {
		return errors.New("OpenCode part event is malformed")
	}
	switch value.Part.Type {
	case "text":
		if value.Part.ID == "" {
			return errors.New("OpenCode text part has no id")
		}
		current := s.textByPart[value.Part.ID]
		if !strings.HasPrefix(value.Part.Text, current) {
			return errors.New("OpenCode final text does not match its streamed text")
		}
		return s.appendText(value.Part.ID, strings.TrimPrefix(value.Part.Text, current))
	case "tool":
		s.toolEvent(value.Part.ID, value.Part.Tool, value.Part.State.Status, len(value.Part.State.Attachments))
	case "step-finish":
		s.addUsage(value.Part.Tokens, value.Part.Cost, value.Part.Reason)
	case "patch":
		s.artifactEvent(value.Part.ID, "patch", len(value.Part.Files))
	}
	return nil
}

func (s *eventState) processMessageUpdated(raw json.RawMessage) error {
	var value struct {
		Info struct {
			Role   string          `json:"role"`
			Cost   *float64        `json:"cost"`
			Finish string          `json:"finish"`
			Tokens *wireTokens     `json:"tokens"`
			Error  json.RawMessage `json:"error"`
		} `json:"info"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("OpenCode message event is malformed")
	}
	if value.Info.Role != "assistant" {
		return nil
	}
	s.setUsage(value.Info.Tokens, value.Info.Cost, value.Info.Finish)
	if len(value.Info.Error) > 0 && string(value.Info.Error) != "null" {
		s.setFailure(value.Info.Error)
	}
	return nil
}

func (s *eventState) rejectPermission(ctx context.Context, runtime *Runtime, server, username, password string, raw json.RawMessage) error {
	var value struct {
		ID         string `json:"id"`
		Permission string `json:"permission"`
	}
	if json.Unmarshal(raw, &value) != nil || !validOpaqueID(value.ID, "per") {
		return errors.New("OpenCode permission request is malformed")
	}
	name := safeName(value.Permission)
	s.emitEvent(execution.RuntimeEvent{Kind: execution.RuntimeApprovalRequested, Name: name, Status: "requested"})
	reply, _ := json.Marshal(map[string]string{"reply": "reject", "message": "Veto does not auto-approve runtime permissions"})
	_, status, err := runtime.request(ctx, http.MethodPost, server+"/permission/"+value.ID+"/reply", username, password, reply)
	if err != nil || (status != http.StatusOK && status != http.StatusNoContent) {
		if err == nil {
			err = fmt.Errorf("HTTP %d", status)
		}
		return fmt.Errorf("reject OpenCode permission request: %w", err)
	}
	s.emitEvent(execution.RuntimeEvent{Kind: execution.RuntimeApprovalDenied, Name: name, Status: "denied"})
	s.failure = fmt.Errorf("OpenCode permission %q was denied; Veto never auto-approves runtime actions", name)
	return nil
}

func (s *eventState) setFailure(raw json.RawMessage) {
	s.failure = parseWireError(raw)
	if wireErrorName(raw) == "MessageOutputLengthError" {
		s.truncated = true
		s.finish = "max_output_tokens"
	}
}

func (s *eventState) appendText(partID, delta string) error {
	if delta == "" {
		return nil
	}
	if !utf8.ValidString(delta) || s.output.Len()+len(delta) > maxEventBytes {
		return errors.New("OpenCode text output exceeds the size limit or is invalid UTF-8")
	}
	if _, err := io.WriteString(s.writer, delta); err != nil {
		return fmt.Errorf("write OpenCode output: %w", err)
	}
	s.textByPart[partID] += delta
	s.output.WriteString(delta)
	return nil
}

func (s *eventState) toolEvent(partID, name, status string, attachments int) {
	if partID == "" || s.toolStates[partID] == status {
		return
	}
	previous := s.toolStates[partID]
	s.toolStates[partID] = status
	if (previous == "pending" || previous == "running") && (status == "pending" || status == "running") {
		return
	}
	event := execution.RuntimeEvent{Name: safeName(name), Status: status}
	switch status {
	case "pending", "running":
		event.Kind = execution.RuntimeToolStarted
	case "completed":
		event.Kind = execution.RuntimeToolCompleted
	case "error":
		event.Kind = execution.RuntimeToolError
	default:
		return
	}
	s.emitEvent(event)
	if attachments > 0 {
		s.artifactEvent(partID+":attachments", "attachment", attachments)
	}
}

func (s *eventState) artifactEvent(id, kind string, count int) {
	if id == "" || count <= 0 || s.artifacts[id] {
		return
	}
	s.artifacts[id] = true
	s.emitEvent(execution.RuntimeEvent{Kind: execution.RuntimeArtifactCreated, Name: kind, Status: "created", Count: count})
}

func (s *eventState) emitEvent(event execution.RuntimeEvent) {
	if s.emit != nil {
		s.emit(event)
	}
}

func (s *eventState) addUsage(tokens *wireTokens, cost *float64, finish string) {
	if tokens != nil {
		s.usage.Known = true
		s.usage.InputTokens += nonNegativeInt(tokens.Input)
		s.usage.OutputTokens += nonNegativeInt(tokens.Output)
		if tokens.Total != nil {
			s.usage.TotalTokens += nonNegativeInt(*tokens.Total)
		} else {
			s.usage.TotalTokens += nonNegativeInt(tokens.Input + tokens.Output)
		}
	}
	if cost != nil {
		s.cost += *cost
		s.costKnown = true
	}
	s.setFinish(finish)
}

func (s *eventState) setUsage(tokens *wireTokens, cost *float64, finish string) {
	if tokens != nil {
		s.usage = execution.Usage{InputTokens: nonNegativeInt(tokens.Input), OutputTokens: nonNegativeInt(tokens.Output), Known: true}
		if tokens.Total != nil {
			s.usage.TotalTokens = nonNegativeInt(*tokens.Total)
		} else {
			s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
		}
	}
	if cost != nil {
		s.cost, s.costKnown = *cost, true
	}
	s.setFinish(finish)
}

func (s *eventState) setFinish(finish string) {
	if finish == "" {
		return
	}
	s.finish = finish
	s.truncated = finish == "length" || finish == "max_tokens" || finish == "max_output_tokens"
}

func (s *eventState) result(err error) execution.Result {
	return execution.Result{
		Output: s.output.String(), Error: err, Usage: s.usage,
		CostUSD: s.cost, CostKnown: s.costKnown,
		FinishReason: s.finish, Truncated: s.truncated,
	}
}

func (r *Runtime) executeCLI(
	ctx context.Context,
	purpose sessionPurpose,
	prompt string,
	_ execution.ExecutionOptions,
	w io.Writer,
	emit func(execution.RuntimeEvent),
) execution.Result {
	path := r.discovery.Executable
	if path == "" {
		var err error
		path, err = r.deps.LookPath("opencode")
		if err != nil {
			return execution.Result{Error: fmt.Errorf("OpenCode CLI is not available on PATH: %w", err)}
		}
	}
	title, err := internalSessionTitle(purpose)
	if err != nil {
		return execution.Result{Error: err}
	}
	args := []string{"run", "--format", "json", "--model", r.model.Provider + "/" + r.model.ID, "--title", title, "--", prompt}
	var env []string
	if purpose == purposeAdmission {
		inline, envErr := admissionConfigContent(r.deps.Getenv("OPENCODE_CONFIG_CONTENT"))
		if envErr != nil {
			return execution.Result{Error: envErr}
		}
		env = []string{"OPENCODE_CONFIG_CONTENT=" + inline}
	}

	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdoutReader, stdoutWriter := io.Pipe()
	var stderr limitedBuffer
	commandDone := make(chan error, 1)
	go func() {
		runErr := r.deps.Stream(commandCtx, path, args, env, stdoutWriter, &stderr)
		_ = stdoutWriter.CloseWithError(runErr)
		commandDone <- runErr
	}()

	state := eventState{writer: w, emit: emit, textByPart: make(map[string]string), toolStates: make(map[string]string), artifacts: make(map[string]bool)}
	scanner := bufio.NewScanner(stdoutReader)
	scanner.Buffer(make([]byte, 64*1024), maxEventLine)
	total := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		total += len(line) + 1
		if total > maxCommandBytes {
			cancel()
			return state.result(errors.New("OpenCode CLI event output exceeds the size limit"))
		}
		if err := state.processCLI(line); err != nil {
			cancel()
			return state.result(err)
		}
	}
	scanErr := scanner.Err()
	runErr := <-commandDone
	if ctx.Err() != nil {
		return state.result(ctx.Err())
	}
	if scanErr != nil && !errors.Is(scanErr, runErr) {
		return state.result(fmt.Errorf("read OpenCode CLI events: %w", scanErr))
	}
	if runErr != nil {
		message := strings.TrimSpace(stderr.buffer.String())
		if message != "" {
			runErr = fmt.Errorf("%w: %s", runErr, safeErrorMessage(message))
		}
		return state.result(fmt.Errorf("run OpenCode CLI: %w", runErr))
	}
	return state.result(state.failure)
}

func (s *eventState) processCLI(line []byte) error {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var event struct {
		Type string `json:"type"`
		Part struct {
			ID     string      `json:"id"`
			Type   string      `json:"type"`
			Text   string      `json:"text"`
			Tool   string      `json:"tool"`
			Reason string      `json:"reason"`
			Cost   *float64    `json:"cost"`
			Tokens *wireTokens `json:"tokens"`
			State  struct {
				Status      string            `json:"status"`
				Attachments []json.RawMessage `json:"attachments"`
			} `json:"state"`
		} `json:"part"`
		Error json.RawMessage `json:"error"`
	}
	if !utf8.Valid(line) || json.Unmarshal(line, &event) != nil || event.Type == "" {
		return errors.New("OpenCode CLI returned a malformed JSON event")
	}
	switch event.Type {
	case "text":
		id := event.Part.ID
		if id == "" {
			id = fmt.Sprintf("text-%d", len(s.textByPart)+1)
		}
		return s.appendText(id, event.Part.Text)
	case "tool_use":
		s.toolEvent(event.Part.ID, event.Part.Tool, event.Part.State.Status, len(event.Part.State.Attachments))
	case "step_finish":
		s.addUsage(event.Part.Tokens, event.Part.Cost, event.Part.Reason)
	case "error":
		s.failure = parseWireError(event.Error)
	}
	return nil
}

func setBasicAuth(request *http.Request, username, password string) {
	if password == "" {
		return
	}
	if username == "" {
		username = "opencode"
	}
	request.SetBasicAuth(username, password)
}

func internalSessionTitle(purpose sessionPurpose) (string, error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("create OpenCode session id: %w", err)
	}
	return "veto:" + string(purpose) + ":" + hex.EncodeToString(id[:]), nil
}

func validSessionID(id string) bool {
	return validOpaqueID(id, "ses")
}

func validOpaqueID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 || !validIdentifier(value) {
		return "unknown"
	}
	return value
}

func admissionConfigContent(existing string) (string, error) {
	config := make(map[string]json.RawMessage)
	if strings.TrimSpace(existing) != "" {
		if json.Unmarshal([]byte(existing), &config) != nil || config == nil {
			return "", errors.New("OPENCODE_CONFIG_CONTENT is malformed; refusing to replace it for admission")
		}
	}
	permission, err := json.Marshal(map[string]string{"*": "deny"})
	if err != nil {
		return "", err
	}
	config["permission"] = permission
	out, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseWireError(raw json.RawMessage) error {
	var value struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return errors.New("OpenCode session failed")
	}
	message := value.Data.Message
	if message == "" {
		message = value.Message
	}
	message = safeErrorMessage(message)
	if value.Name == "MessageOutputLengthError" {
		return errors.New("OpenCode output length limit was reached")
	}
	if message == "" {
		message = "session failed"
	}
	if value.Name != "" {
		return fmt.Errorf("OpenCode %s: %s", safeName(value.Name), message)
	}
	return errors.New("OpenCode: " + message)
}

func wireErrorName(raw json.RawMessage) string {
	var value struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Name
}

func safeErrorMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	return ledger.Redact(message)
}

func nonNegativeInt(value float64) int {
	if value <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if value >= float64(maxInt) {
		return maxInt
	}
	return int(value)
}
