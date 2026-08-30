package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerRuntimeStreamsEventsUsageArtifactsAndCleansUp(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.delta", map[string]any{
			"sessionID": sessionID, "messageID": "msg_1", "partID": "prt_text", "field": "text", "delta": "hello ",
		})
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID,
			"part":      map[string]any{"id": "prt_tool", "type": "tool", "tool": "bash", "state": map[string]any{"status": "running"}},
		})
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID,
			"part": map[string]any{"id": "prt_tool", "type": "tool", "tool": "bash", "state": map[string]any{
				"status": "completed", "attachments": []map[string]any{{"type": "file"}},
			}},
		})
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID,
			"part":      map[string]any{"id": "prt_patch", "type": "patch", "files": []string{"secret-path", "another-path"}},
		})
		f.send(sessionID, "message.part.delta", map[string]any{
			"sessionID": sessionID, "messageID": "msg_1", "partID": "prt_text", "field": "text", "delta": "world",
		})
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID,
			"part": map[string]any{"id": "prt_finish", "type": "step-finish", "reason": "stop", "cost": 0.0125,
				"tokens": map[string]any{"total": 15, "input": 10, "output": 5, "reasoning": 0, "cache": map[string]int{"read": 0, "write": 0}}},
		})
		f.send(sessionID, "session.status", map[string]any{"sessionID": sessionID, "status": map[string]string{"type": "idle"}})
	})
	defer fake.Close()

	runtime := NewRuntime(
		Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)},
		Discovery{Mode: ModeAttach, Server: loopbackURL(t, fake.URL), Version: "1.18.5"},
		Model{Provider: "openai", ID: "gpt-5"},
		Dependencies{Do: fake.Client().Do},
	)
	var output bytes.Buffer
	var events []executor.RuntimeEvent
	result := runtime.ExecuteWithEvents(context.Background(), "do work", executor.ExecutionOptions{}, &output, func(event executor.RuntimeEvent) {
		events = append(events, event)
	})

	require.NoError(t, result.Error)
	assert.Equal(t, "hello world", output.String())
	assert.Equal(t, output.String(), result.Output)
	assert.Equal(t, executor.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Known: true}, result.Usage)
	assert.True(t, result.CostKnown)
	assert.InDelta(t, 0.0125, result.CostUSD, 0.000001)
	assert.Equal(t, "stop", result.FinishReason)
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeToolStarted, Name: "bash", Status: "running"})
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeToolCompleted, Name: "bash", Status: "completed"})
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeArtifactCreated, Name: "attachment", Status: "created", Count: 1})
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeArtifactCreated, Name: "patch", Status: "created", Count: 2})
	assert.Equal(t, int64(1), fake.deleted.Load())
	assert.Empty(t, fake.lastCreate["permission"], "execution must preserve OpenCode's configured permission policy")
	assert.True(t, strings.HasPrefix(fake.lastCreate["title"].(string), "veto:execution:"))
	assert.Equal(t, map[string]any{"providerID": "openai", "modelID": "gpt-5"}, fake.lastPrompt["model"])
}

func TestServerAdmissionIsIsolatedIdentifiableAndToolDenied(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID, "part": map[string]any{"id": "prt_text", "type": "text", "text": `{"accept":true}`},
		})
		f.send(sessionID, "session.idle", map[string]any{"sessionID": sessionID})
	})
	defer fake.Close()

	runtime := NewRuntime(Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{Do: fake.Client().Do})
	result := runtime.Run(context.Background(), "admit this")

	require.NoError(t, result.Error)
	assert.Equal(t, `{"accept":true}`, result.Output)
	assert.True(t, strings.HasPrefix(fake.lastCreate["title"].(string), "veto:admission:"))
	permission, ok := fake.lastCreate["permission"].([]any)
	require.True(t, ok)
	require.Len(t, permission, 1)
	assert.Equal(t, "deny", permission[0].(map[string]any)["action"])
	assert.Equal(t, int64(1), fake.deleted.Load())
}

func TestManagedRuntimeOwnsServerForOneFreshExecution(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID, "part": map[string]any{"id": "prt_text", "type": "text", "text": "managed"},
		})
		f.send(sessionID, "session.idle", map[string]any{"sessionID": sessionID})
	})
	defer fake.Close()
	process := &fakeProcess{wait: make(chan error, 1)}
	deps := Dependencies{
		Do:       fake.Client().Do,
		LookPath: func(string) (string, error) { return "/safe/opencode", nil },
		Start: func(_ string, args, env []string) (Process, error) {
			assert.Equal(t, "serve", args[0])
			assert.Contains(t, strings.Join(env, "\n"), "OPENCODE_SERVER_PASSWORD=")
			return process, nil
		},
	}
	runtime := NewRuntime(Config{Mode: ModeManaged, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, deps)

	result := runtime.Execute(context.Background(), "work", executor.ExecutionOptions{})
	require.NoError(t, result.Error)
	assert.Equal(t, "managed", result.Output)
	assert.True(t, process.killed)
}

func TestServerRuntimeRejectsPermissionAndPreservesPartialOutput(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.delta", map[string]any{
			"sessionID": sessionID, "messageID": "msg_1", "partID": "prt_text", "field": "text", "delta": "partial",
		})
		f.send(sessionID, "permission.asked", map[string]any{
			"id": "per_1", "sessionID": sessionID, "permission": "bash", "patterns": []string{"sensitive command"}, "metadata": map[string]any{}, "always": []string{},
		})
		f.send(sessionID, "session.status", map[string]any{"sessionID": sessionID, "status": map[string]string{"type": "idle"}})
	})
	defer fake.Close()

	runtime := NewRuntime(Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{Do: fake.Client().Do})
	var events []executor.RuntimeEvent
	result := runtime.ExecuteWithEvents(context.Background(), "work", executor.ExecutionOptions{}, io.Discard, func(event executor.RuntimeEvent) {
		events = append(events, event)
	})

	require.Error(t, result.Error)
	assert.ErrorContains(t, result.Error, "never auto-approves")
	assert.Equal(t, "partial", result.Output)
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeApprovalRequested, Name: "bash", Status: "requested"})
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeApprovalDenied, Name: "bash", Status: "denied"})
	assert.Equal(t, int64(1), fake.permissionRejected.Load())
}

func TestServerRuntimeMapsOutputLengthFailureAndPartialOutput(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.delta", map[string]any{
			"sessionID": sessionID, "messageID": "msg_1", "partID": "prt_text", "field": "text", "delta": "partial",
		})
		f.send(sessionID, "session.error", map[string]any{
			"sessionID": sessionID, "error": map[string]any{"name": "MessageOutputLengthError", "data": map[string]any{}},
		})
		f.send(sessionID, "session.idle", map[string]any{"sessionID": sessionID})
	})
	defer fake.Close()
	runtime := NewRuntime(Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{Do: fake.Client().Do})

	result := runtime.Execute(context.Background(), "work", executor.ExecutionOptions{})
	require.Error(t, result.Error)
	assert.Equal(t, "partial", result.Output)
	assert.True(t, result.Truncated)
	assert.Equal(t, "max_output_tokens", result.FinishReason)
}

func TestServerRuntimeCancellationAbortsAndDeletesSession(t *testing.T) {
	prompted := make(chan struct{})
	fake := newFakeSessionServer(t, func(_ *fakeSessionServer, _ string) { close(prompted) })
	defer fake.Close()
	runtime := NewRuntime(Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{Do: fake.Client().Do})

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan executor.Result, 1)
	go func() { resultCh <- runtime.Execute(ctx, "wait", executor.ExecutionOptions{}) }()
	<-prompted
	cancel()
	result := <-resultCh

	assert.ErrorIs(t, result.Error, context.Canceled)
	assert.Equal(t, int64(1), fake.aborted.Load())
	assert.Equal(t, int64(1), fake.deleted.Load())
}

func TestServerRuntimeDoesNotResumeStaleSessionsAfterRestart(t *testing.T) {
	fake := newFakeSessionServer(t, func(f *fakeSessionServer, sessionID string) {
		f.send(sessionID, "message.part.updated", map[string]any{
			"sessionID": sessionID, "part": map[string]any{"id": "prt_text", "type": "text", "text": "ok"},
		})
		f.send(sessionID, "session.idle", map[string]any{"sessionID": sessionID})
	})
	defer fake.Close()
	runtime := NewRuntime(Config{Mode: ModeAttach, Server: loopbackURL(t, fake.URL)}, Discovery{}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{Do: fake.Client().Do})

	first := runtime.Execute(context.Background(), "one", executor.ExecutionOptions{})
	second := runtime.Execute(context.Background(), "two", executor.ExecutionOptions{})

	require.NoError(t, first.Error)
	require.NoError(t, second.Error)
	require.Len(t, fake.createdIDs, 2)
	assert.NotEqual(t, fake.createdIDs[0], fake.createdIDs[1])
	assert.Equal(t, int64(2), fake.deleted.Load())
}

func TestCLIRuntimeUsesExactModelStreamsJSONAndNeverPassesAuto(t *testing.T) {
	var gotPath string
	var gotArgs, gotEnv []string
	deps := Dependencies{
		Stream: func(_ context.Context, path string, args, env []string, stdout, _ io.Writer) error {
			gotPath, gotArgs, gotEnv = path, append([]string(nil), args...), append([]string(nil), env...)
			_, _ = io.WriteString(stdout, `{"type":"text","sessionID":"ses_1","part":{"id":"prt_1","text":"hello"}}`+"\n")
			_, _ = io.WriteString(stdout, `{"type":"tool_use","sessionID":"ses_1","part":{"id":"prt_2","tool":"read","state":{"status":"completed"}}}`+"\n")
			_, _ = io.WriteString(stdout, `{"type":"step_finish","sessionID":"ses_1","part":{"reason":"stop","cost":0.01,"tokens":{"total":7,"input":5,"output":2}}}`+"\n")
			return nil
		},
	}
	runtime := NewRuntime(Config{Mode: ModeCLI}, Discovery{Executable: "/safe/opencode"}, Model{Provider: "openrouter", ID: "openai/gpt-5"}, deps)
	var output bytes.Buffer
	var events []executor.RuntimeEvent
	result := runtime.ExecuteWithEvents(context.Background(), "--auto must be data", executor.ExecutionOptions{}, &output, func(event executor.RuntimeEvent) {
		events = append(events, event)
	})

	require.NoError(t, result.Error)
	assert.Equal(t, "/safe/opencode", gotPath)
	assert.Equal(t, []string{"run", "--format", "json", "--model", "openrouter/openai/gpt-5", "--title"}, gotArgs[:6])
	assert.True(t, strings.HasPrefix(gotArgs[6], "veto:execution:"))
	assert.Equal(t, []string{"--", "--auto must be data"}, gotArgs[7:])
	assert.NotContains(t, gotArgs[:7], "--auto")
	assert.Empty(t, gotEnv)
	assert.Equal(t, "hello", output.String())
	assert.Equal(t, output.String(), result.Output)
	assert.Equal(t, executor.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, Known: true}, result.Usage)
	assert.True(t, result.CostKnown)
	assert.Contains(t, events, executor.RuntimeEvent{Kind: executor.RuntimeToolCompleted, Name: "read", Status: "completed"})
}

func TestCLIAdmissionUsesDenyOverrideAndIdentifiableSession(t *testing.T) {
	var gotArgs, gotEnv []string
	runtime := NewRuntime(Config{Mode: ModeCLI}, Discovery{Executable: "/safe/opencode"}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{
		Getenv: func(string) string { return "" },
		Stream: func(_ context.Context, _ string, args, env []string, stdout, _ io.Writer) error {
			gotArgs, gotEnv = append([]string(nil), args...), append([]string(nil), env...)
			_, _ = io.WriteString(stdout, `{"type":"text","part":{"id":"prt_1","text":"{}"}}`+"\n")
			return nil
		},
	})
	result := runtime.Run(context.Background(), "admission")

	require.NoError(t, result.Error)
	assert.True(t, strings.HasPrefix(gotArgs[6], "veto:admission:"))
	assert.Equal(t, []string{`OPENCODE_CONFIG_CONTENT={"permission":{"*":"deny"}}`}, gotEnv)
	assert.NotContains(t, gotArgs, "--auto")
}

func TestCLIRuntimeCancellationAndPartialFailure(t *testing.T) {
	started := make(chan struct{})
	runtime := NewRuntime(Config{Mode: ModeCLI}, Discovery{Executable: "/safe/opencode"}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{
		Stream: func(ctx context.Context, _ string, _ []string, _ []string, stdout, _ io.Writer) error {
			_, _ = io.WriteString(stdout, `{"type":"text","part":{"id":"prt_1","text":"partial"}}`+"\n")
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan executor.Result, 1)
	go func() { resultCh <- runtime.Execute(ctx, "work", executor.ExecutionOptions{}) }()
	<-started
	cancel()
	result := <-resultCh
	assert.ErrorIs(t, result.Error, context.Canceled)
	assert.Equal(t, "partial", result.Output)
}

type fakeSessionServer struct {
	*httptest.Server
	t                  *testing.T
	events             chan []byte
	onPrompt           func(*fakeSessionServer, string)
	sequence           atomic.Int64
	deleted            atomic.Int64
	aborted            atomic.Int64
	permissionRejected atomic.Int64
	mu                 sync.Mutex
	lastCreate         map[string]any
	lastPrompt         map[string]any
	createdIDs         []string
}

func newFakeSessionServer(t *testing.T, onPrompt func(*fakeSessionServer, string)) *fakeSessionServer {
	t.Helper()
	fake := &fakeSessionServer{t: t, events: make(chan []byte, 64), onPrompt: onPrompt}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	return fake
}

func (f *fakeSessionServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/global/health":
		_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.5"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/provider":
		_, _ = io.WriteString(w, `{"all":[{"id":"openai","models":{"gpt-5":{"id":"gpt-5"}}}],"connected":["openai"],"default":{}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/session":
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		id := fmt.Sprintf("ses_test_%d", f.sequence.Add(1))
		f.mu.Lock()
		f.lastCreate = body
		f.createdIDs = append(f.createdIDs, id)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	case r.Method == http.MethodGet && r.URL.Path == "/global/event":
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(f.t, ok)
		_, _ = io.WriteString(w, "event: message\ndata: {\"payload\":{\"type\":\"server.connected\",\"properties\":{}}}\n\n")
		flusher.Flush()
		for {
			select {
			case event := <-f.events:
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", event)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.mu.Lock()
		f.lastPrompt = body
		f.mu.Unlock()
		sessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/prompt_async")
		w.WriteHeader(http.StatusNoContent)
		go f.onPrompt(f, sessionID)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
		f.aborted.Add(1)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/session/"):
		f.deleted.Add(1)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/permission/per_1/reply":
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		if body["reply"] == "reject" {
			f.permissionRejected.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSessionServer) send(_ string, eventType string, properties map[string]any) {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"payload": map[string]any{"type": eventType, "properties": properties}})
	require.NoError(f.t, err)
	f.events <- body
}

func TestParseWireErrorDoesNotExposeResponseBody(t *testing.T) {
	err := parseWireError(json.RawMessage(`{"name":"APIError","data":{"message":"safe summary","responseBody":"credential=secret"}}`))
	assert.EqualError(t, err, "OpenCode APIError: safe summary")
	assert.NotContains(t, err.Error(), "secret")
}

func TestAdmissionConfigContentPreservesExistingInlineConfig(t *testing.T) {
	got, err := admissionConfigContent(`{"provider":{"custom":{"token":"secret"}},"permission":{"bash":"allow"}}`)
	require.NoError(t, err)
	var config map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(got), &config))
	assert.JSONEq(t, `{"custom":{"token":"secret"}}`, string(config["provider"]))
	assert.JSONEq(t, `{"*":"deny"}`, string(config["permission"]))

	_, err = admissionConfigContent(`null`)
	require.Error(t, err)
}

func TestSafeErrorMessageRedactsCredentials(t *testing.T) {
	assert.Equal(t, "authorization=[REDACTED]", safeErrorMessage("authorization=Bearer-secret"))
}

func TestCLIRuntimeRejectsMalformedEvents(t *testing.T) {
	runtime := NewRuntime(Config{Mode: ModeCLI}, Discovery{Executable: "/safe/opencode"}, Model{Provider: "openai", ID: "gpt-5"}, Dependencies{
		Stream: func(_ context.Context, _ string, _ []string, _ []string, stdout, _ io.Writer) error {
			_, _ = io.WriteString(stdout, "not-json\n")
			return errors.New("stopped")
		},
	})
	result := runtime.Execute(context.Background(), "work", executor.ExecutionOptions{})
	require.Error(t, result.Error)
	assert.ErrorContains(t, result.Error, "malformed JSON event")
}

func TestSafeNameRejectsControlCharacters(t *testing.T) {
	assert.Equal(t, "unknown", safeName("bash\nsecret"))
	assert.Equal(t, "bash", safeName("bash"))
}

func TestOpaqueIDsRejectPathInjection(t *testing.T) {
	assert.True(t, validSessionID("ses_safe_123"))
	assert.False(t, validSessionID("ses/../../permission"))
	assert.False(t, validOpaqueID("per?reply=always", "per"))
}

func TestToolPendingAndRunningEmitOneStart(t *testing.T) {
	var events []executor.RuntimeEvent
	state := eventState{emit: func(event executor.RuntimeEvent) { events = append(events, event) }, toolStates: map[string]string{}, artifacts: map[string]bool{}}
	state.toolEvent("prt_1", "bash", "pending", 0)
	state.toolEvent("prt_1", "bash", "running", 0)
	assert.Equal(t, []executor.RuntimeEvent{{Kind: executor.RuntimeToolStarted, Name: "bash", Status: "pending"}}, events)
}

func TestInternalSessionTitleIsOpaque(t *testing.T) {
	title, err := internalSessionTitle(purposeAdmission)
	require.NoError(t, err)
	assert.Regexp(t, `^veto:admission:[0-9a-f]{16}$`, title)
	assert.NotContains(t, title, "objective")
}

func TestEventStreamLimitIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", maxEventLine+1)+"\n\n")
	}))
	defer server.Close()
	runtime := NewRuntime(Config{}, Discovery{}, Model{}, Dependencies{Do: server.Client().Do})
	ready := make(chan error, 1)
	err := runtime.readEvents(context.Background(), server.URL, "", "", ready, make(chan wireEvent, 1))
	require.NoError(t, <-ready)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token too long")
}

func TestRuntimeInterfaces(t *testing.T) {
	var runtime any = NewRuntime(Config{}, Discovery{}, Model{}, Dependencies{})
	_, ok := runtime.(executor.RuntimeAdapter)
	assert.True(t, ok)
	_, ok = runtime.(executor.EventTaskExecutor)
	assert.True(t, ok)
}

func TestFakeServerConcurrentFieldsAreReadable(t *testing.T) {
	// Keeps the fake's mutex-protected fields covered by the race build.
	fake := &fakeSessionServer{}
	fake.mu.Lock()
	fake.createdIDs = append(fake.createdIDs, "ses_test")
	fake.mu.Unlock()
	assert.Equal(t, []string{"ses_test"}, fake.createdIDs)
}
