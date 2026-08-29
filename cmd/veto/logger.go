package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
)

var routeLog *slog.Logger

// setupLogger opens today's log file, rotates old ones, and sets routeLog.
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
		routeLog = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		return
	}
	routeLog = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// logEvent writes a routing pipeline event as a structured JSON log line.
func logEvent(task, kind, risk string, e router.ProgressEvent) {
	if routeLog == nil {
		return
	}
	attrs := []any{
		slog.String("task_kind", kind),
		slog.String("task_risk", risk),
		slog.String("task_obj", truncate(task, 120)),
		slog.String("event", string(e.Kind)),
		slog.String("model", e.Model),
	}
	if len(e.Reasons) > 0 {
		attrs = append(attrs, slog.String("reasons", strings.Join(e.Reasons, ",")))
	}
	if e.Confidence > 0 {
		attrs = append(attrs, slog.Float64("confidence", e.Confidence))
	}
	if e.EstCost > 0 {
		attrs = append(attrs, slog.Float64("est_cost_usd", e.EstCost))
	}
	if e.Detail != "" {
		attrs = append(attrs, slog.String("detail", normalizeErrorDetail(e.Detail)))
	}
	routeLog.Debug("route_event", attrs...)
}

func normalizeErrorDetail(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	return truncate(detail, 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
