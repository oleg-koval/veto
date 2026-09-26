// Package shadowhistory persists redacted shadow evidence as private JSONL.
package shadowhistory

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/oleg-koval/veto/pkg/router"
	shadowdata "github.com/oleg-koval/veto/pkg/shadow"
)

const DefaultMaxEvents = 2000

// ErrEvidenceLimit means the bounded append-only stream is full. Routing must
// ignore this recorder error; an operator can archive or remove the local file.
var ErrEvidenceLimit = errors.New("shadow evidence event limit reached")

// FileRecorder is safe for concurrent use.
type FileRecorder struct {
	mu        sync.Mutex
	path      string
	maxEvents int
	counted   bool
	events    int
	failed    error
}

func NewFileRecorder(path string, maxEvents int) *FileRecorder {
	if maxEvents <= 0 {
		maxEvents = DefaultMaxEvents
	}
	return &FileRecorder{path: path, maxEvents: maxEvents}
}

func (r *FileRecorder) RecordShadowComparison(record router.ShadowComparisonRecord) error {
	candidates := make([]shadowdata.Candidate, 0, len(record.Candidates))
	for _, key := range record.Candidates {
		candidates = append(candidates, shadowdata.Candidate{Key: key})
	}
	comparison := shadowdata.RouteComparison{
		RouteID: record.RouteID, ObservedAt: record.ObservedAt, TaskKind: string(record.TaskKind), Risk: string(record.Risk),
		Candidates: candidates, AuthorityStrategy: record.AuthorityStrategy, ShadowStrategy: record.ShadowStrategy,
		Authority: decision(record.Authority), Shadow: decision(record.Shadow),
	}
	return r.append(shadowdata.Event{SchemaVersion: shadowdata.SchemaVersion, Type: shadowdata.EventRouteComparison, Comparison: &comparison})
}

func (r *FileRecorder) RecordShadowExecutionLabel(record router.ShadowExecutionLabelRecord) error {
	label := shadowdata.ExecutionLabel{
		RouteID: record.RouteID, ObservedAt: record.ObservedAt, Candidate: record.Candidate,
		Success: shadowdata.KnownBool{Known: record.Success.Known, Value: record.Success.Value},
		Score:   shadowdata.KnownFloat{Known: record.Score.Known, Value: record.Score.Value},
		Usage:   shadowdata.KnownUsage{Known: record.Telemetry.UsageKnown, InputTokens: record.Telemetry.InputTokens, OutputTokens: record.Telemetry.OutputTokens, TotalTokens: record.Telemetry.TotalTokens},
		CostUSD: shadowdata.KnownFloat{Known: record.Telemetry.CostKnown, Value: record.Telemetry.CostUSD},
		Latency: shadowdata.KnownDuration{Known: record.Telemetry.LatencyKnown, Millis: record.Telemetry.LatencyMs},
	}
	return r.append(shadowdata.Event{SchemaVersion: shadowdata.SchemaVersion, Type: shadowdata.EventExecutionLabel, Label: &label})
}

func decision(record router.ShadowDecisionRecord) shadowdata.DecisionEvidence {
	return shadowdata.DecisionEvidence{
		Status: shadowdata.DecisionStatus(record.Status), SelectedCandidate: record.SelectedCandidate,
		Probability: shadowdata.KnownFloat{Known: record.Probability.Known, Value: record.Probability.Value},
		Confidence:  shadowdata.KnownFloat{Known: record.Confidence.Known, Value: record.Confidence.Value},
		Telemetry: shadowdata.Telemetry{
			Usage:   shadowdata.KnownUsage{Known: record.Telemetry.UsageKnown, InputTokens: record.Telemetry.InputTokens, OutputTokens: record.Telemetry.OutputTokens, TotalTokens: record.Telemetry.TotalTokens},
			CostUSD: shadowdata.KnownFloat{Known: record.Telemetry.CostKnown, Value: record.Telemetry.CostUSD},
			Latency: shadowdata.KnownDuration{Known: record.Telemetry.LatencyKnown, Millis: record.Telemetry.LatencyMs},
		},
		ErrorCode: record.ErrorCode,
	}
}

func (r *FileRecorder) append(event shadowdata.Event) error {
	var line bytes.Buffer
	if err := shadowdata.Append(&line, event); err != nil {
		r.poison(err)
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return r.failed
	}
	if err := r.ensureCount(); err != nil {
		r.failed = err
		return err
	}
	if r.events >= r.maxEvents {
		return ErrEvidenceLimit
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		r.failed = fmt.Errorf("shadow evidence: create directory: %w", err)
		return r.failed
	}
	file, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		r.failed = fmt.Errorf("shadow evidence: open: %w", err)
		return r.failed
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		r.failed = fmt.Errorf("shadow evidence: permissions: %w", err)
		return r.failed
	}
	if _, err := file.Write(line.Bytes()); err != nil {
		_ = file.Close()
		r.failed = fmt.Errorf("shadow evidence: append: %w", err)
		return r.failed
	}
	if err := file.Close(); err != nil {
		r.failed = fmt.Errorf("shadow evidence: close: %w", err)
		return r.failed
	}
	r.events++
	return nil
}

func (r *FileRecorder) poison(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed == nil {
		r.failed = err
	}
}

func (r *FileRecorder) ensureCount() error {
	if r.counted {
		return nil
	}
	file, err := os.Open(r.path)
	if os.IsNotExist(err) {
		r.counted = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("shadow evidence: inspect: %w", err)
	}
	defer file.Close()
	events, err := shadowdata.Load(file)
	if err != nil {
		return err
	}
	r.events = len(events)
	r.counted = true
	return nil
}

var _ router.ShadowEvidenceRecorder = (*FileRecorder)(nil)
