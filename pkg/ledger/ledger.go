// Package ledger defines Veto's versioned, redacted lifecycle event stream.
package ledger

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SchemaVersion is the current ledger event schema version.
const SchemaVersion = 1

// EventType identifies the category of a lifecycle event.
type EventType string

// EventType constants define the lifecycle event taxonomy.
const (
	EventFilterPass         EventType = "route.filter_pass"
	EventFilterFail         EventType = "route.filter_fail"
	EventAdmissionStarted   EventType = "admission.started"
	EventAdmissionAccepted  EventType = "admission.accepted"
	EventAdmissionRejected  EventType = "admission.rejected"
	EventAdmissionError     EventType = "admission.error"
	EventExecutionStarted   EventType = "execution.started"
	EventExecutionCompleted EventType = "execution.completed"
	EventExecutionError     EventType = "execution.error"
	EventToolStarted        EventType = "tool.started"
	EventToolCompleted      EventType = "tool.completed"
	EventToolError          EventType = "tool.error"
	EventApprovalRequested  EventType = "approval.requested"
	EventApprovalGranted    EventType = "approval.granted"
	EventApprovalDenied     EventType = "approval.denied"
	EventArtifactCreated    EventType = "artifact.created"
	EventReviewStarted      EventType = "review.started"
	EventReviewCompleted    EventType = "review.completed"
	EventReviewError        EventType = "review.error"
	EventGoalStarted        EventType = "goal.started"
	EventGoalStep           EventType = "goal.step"
	EventGoalCompleted      EventType = "goal.completed"
	EventGoalStopped        EventType = "goal.stopped"
)

// Event is the allowlisted JSON envelope persisted as one line. It deliberately
// has no objective, prompt, response, credential, cookie, or browser-content
// field; those values do not belong in diagnostic telemetry.
type Event struct {
	SchemaVersion    int       `json:"schema_version"`
	Timestamp        time.Time `json:"timestamp"`
	EventID          string    `json:"event_id"`
	RunID            string    `json:"run_id"`
	Type             EventType `json:"type"`
	TaskID           string    `json:"task_id,omitempty"`
	TaskKind         string    `json:"task_kind,omitempty"`
	Risk             string    `json:"risk,omitempty"`
	Model            string    `json:"model,omitempty"`
	Runtime          string    `json:"runtime,omitempty"`
	Status           string    `json:"status,omitempty"`
	Reasons          []string  `json:"reasons,omitempty"`
	Confidence       *float64  `json:"confidence,omitempty"`
	EstimatedTokens  *int      `json:"estimated_tokens,omitempty"`
	EstimatedCostUSD *float64  `json:"estimated_cost_usd,omitempty"`
	Usage            *Usage    `json:"usage,omitempty"`
	CostUSD          *float64  `json:"cost_usd,omitempty"`
	LatencyMS        *int64    `json:"latency_ms,omitempty"`
	Detail           string    `json:"detail,omitempty"`
}

// Usage holds token consumption metrics for a model invocation.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Writer appends versioned events to a line-delimited JSON stream.
type Writer struct {
	mu  sync.Mutex
	out io.Writer
	now func() time.Time
}

// NewWriter creates a Writer that appends events to out.
func NewWriter(out io.Writer) *Writer {
	return &Writer{out: out, now: time.Now}
}

// NewRunID returns a process-run correlation ID suitable for grouping events
// from one veto command invocation. Task IDs remain stable task correlations.
func NewRunID() (string, error) {
	id, err := newID()
	if err != nil {
		return "", fmt.Errorf("ledger run id: %w", err)
	}
	return "run-" + id, nil
}

// Append writes an event to the stream after redacting sensitive detail fields.
func (w *Writer) Append(event Event) error {
	if strings.TrimSpace(event.RunID) == "" {
		return fmt.Errorf("ledger: run_id is required")
	}
	if strings.TrimSpace(string(event.Type)) == "" {
		return fmt.Errorf("ledger: type is required")
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("ledger event id: %w", err)
	}
	event.SchemaVersion = SchemaVersion
	event.Timestamp = w.now().UTC()
	event.EventID = id
	event.Detail = Redact(event.Detail)

	w.mu.Lock()
	defer w.mu.Unlock()
	return json.NewEncoder(w.out).Encode(event)
}

const maxEventLineBytes = 1024 * 1024

// Read replays valid current-schema events and counts corrupt or incompatible
// lines so one damaged record cannot hide the rest of the ledger.
func Read(in io.Reader) ([]Event, int, error) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), maxEventLineBytes)
	var events []Event
	corrupt := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil ||
			event.SchemaVersion != SchemaVersion || event.EventID == "" ||
			event.RunID == "" || event.Type == "" || event.Timestamp.IsZero() {
			corrupt++
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return events, corrupt, fmt.Errorf("ledger read: %w", err)
	}
	return events, corrupt, nil
}

var (
	jsonCredentialPattern = regexp.MustCompile(`(?i)("(?:[a-z0-9_-]*api[_-]?key|authorization|access[_-]?token|token|password|cookie)"\s*:\s*")(?:(?:\\.)|[^"\\])*(")`)
	credentialPattern     = regexp.MustCompile(`(?i)\b([a-z0-9_-]*api[_-]?key|authorization|access[_-]?token|token|password|cookie)\b\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
	urlUserinfoPattern    = regexp.MustCompile(`(?i)(https?://[^:/@\s]*:)[^@\s]+(@)`)
	bearerPattern         = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	knownKeyPattern       = regexp.MustCompile(`(?i)\b(?:sk-(?:ant-|or-)?|xai-)[a-z0-9._-]{4,}`)
)

// Redact removes credentials, API keys, tokens, and passwords from detail text.
func Redact(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	detail = jsonCredentialPattern.ReplaceAllString(detail, `$1[REDACTED]$2`)
	detail = credentialPattern.ReplaceAllString(detail, "$1=[REDACTED]")
	detail = urlUserinfoPattern.ReplaceAllString(detail, `$1[REDACTED]$2`)
	detail = bearerPattern.ReplaceAllString(detail, "Bearer [REDACTED]")
	detail = knownKeyPattern.ReplaceAllString(detail, "[REDACTED]")
	if len(detail) > 500 {
		detail = detail[:500] + "…"
	}
	return detail
}

func newID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
