package router

// EventKind identifies the type of progress event emitted during Manager.Route.
type EventKind string

// EventKind constants define routing pipeline progress event types.
const (
	EventFilterPass EventKind = "filter_pass"
	EventFilterFail EventKind = "filter_fail"
	EventAskStart   EventKind = "ask_start"
	EventAskAccept  EventKind = "ask_accept"
	EventAskReject  EventKind = "ask_reject"
	EventAskError   EventKind = "ask_error"
)

// ProgressEvent is emitted by Manager.Route at each step of the routing pipeline.
// CLI renderers and loggers subscribe via Manager.OnEvent.
type ProgressEvent struct {
	Kind       EventKind
	Model      string
	Reasons    []string
	Confidence float64
	EstTokens  int
	EstCost    float64
	Detail     string // human-readable detail, e.g. the underlying error on EventAskError
}
