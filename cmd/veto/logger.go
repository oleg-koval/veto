package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
)

var eventLedger *ledger.Writer

// setupLogger opens today's log file, rotates old ones, and sets eventLedger.
// Logs are written to ~/.veto/logs/veto-YYYY-MM-DD.log as JSON lines.
func setupLogger() {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".veto", "logs")
	_ = os.MkdirAll(logDir, 0700)

	// rotate: remove files older than 7 days
	cutoff := time.Now().AddDate(0, 0, -7)
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			info, err := e.Info()
			if err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(logDir, e.Name()))
			}
		}
	}

	name := "veto-" + time.Now().Format("2006-01-02") + ".log"
	f, err := os.OpenFile(filepath.Join(logDir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		// fallback: discard logs silently — routing still works
		eventLedger = ledger.NewWriter(io.Discard)
		return
	}
	eventLedger = ledger.NewWriter(f)
}

// logEvent writes a routing pipeline event as a structured JSON log line.
func logEvent(runID, kind, risk string, e router.ProgressEvent) {
	if eventLedger == nil {
		return
	}
	eventType, ok := ledgerType(e.Kind)
	if !ok {
		return
	}
	event := ledger.Event{
		RunID: runID, TaskID: runID, Type: eventType,
		TaskKind: kind, Risk: risk, Model: e.Model,
		Reasons: append([]string(nil), e.Reasons...), Detail: e.Detail,
	}
	if e.Confidence > 0 {
		event.Confidence = &e.Confidence
	}
	if e.EstTokens > 0 {
		event.EstimatedTokens = &e.EstTokens
	}
	if e.EstCost > 0 {
		event.EstimatedCostUSD = &e.EstCost
	}
	_ = eventLedger.Append(event)
}

func logExecution(runID string, eventType ledger.EventType, model router.ModelCapabilities, metrics router.ExecutionMetrics, detail string) {
	if eventLedger == nil {
		return
	}
	event := ledger.Event{
		RunID: runID, TaskID: runID, Type: eventType, Model: model.Name,
		Runtime: model.Runtime, Status: metrics.Status, Detail: detail,
	}
	if metrics.UsageKnown {
		event.Usage = &ledger.Usage{
			InputTokens: metrics.InputTokens, OutputTokens: metrics.OutputTokens, TotalTokens: metrics.TotalTokens,
		}
	}
	if metrics.CostKnown {
		event.CostUSD = &metrics.CostUSD
	}
	if metrics.LatencyKnown {
		event.LatencyMS = &metrics.LatencyMs
	}
	_ = eventLedger.Append(event)
}

func logLifecycle(runID string, eventType ledger.EventType, status, detail string) {
	if eventLedger == nil || runID == "" {
		return
	}
	_ = eventLedger.Append(ledger.Event{
		RunID: runID, TaskID: runID, Type: eventType, Status: status, Detail: detail,
	})
}

func ledgerType(kind router.EventKind) (ledger.EventType, bool) {
	switch kind {
	case router.EventFilterPass:
		return ledger.EventFilterPass, true
	case router.EventFilterFail:
		return ledger.EventFilterFail, true
	case router.EventAskStart:
		return ledger.EventAdmissionStarted, true
	case router.EventAskAccept:
		return ledger.EventAdmissionAccepted, true
	case router.EventAskReject:
		return ledger.EventAdmissionRejected, true
	case router.EventAskError:
		return ledger.EventAdmissionError, true
	}
	return "", false
}

func normalizeErrorDetail(detail string) string {
	detail = ledger.Redact(strings.Join(strings.Fields(detail), " "))
	return truncate(detail, 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
