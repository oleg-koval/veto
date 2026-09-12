// Package verifiedrun defines bounded, redacted evidence and outcome records.
package verifiedrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/ledger"
)

const SchemaVersion = 1

const maxSummaryBytes = 1024

type CriteriaManifest struct {
	Version  int      `json:"version"`
	Criteria []string `json:"criteria"`
}

type EvidenceManifest struct {
	Version  int        `json:"version"`
	Evidence []Evidence `json:"evidence"`
}

// Evidence describes a supplied artifact without storing its content or path.
type Evidence struct {
	ID        string `json:"id"`
	Criterion string `json:"criterion"`
	Type      string `json:"type"`
	Summary   string `json:"summary"`
	SHA256    string `json:"sha256,omitempty"`
}

type Outcome string

const (
	OutcomeVerifiedPass Outcome = "verified_pass"
	OutcomeVerifiedFail Outcome = "verified_fail"
	OutcomeInconclusive Outcome = "inconclusive"
)

type CriterionReceipt struct {
	Criterion     string `json:"criterion"`
	Met           bool   `json:"met"`
	Note          string `json:"note,omitempty"`
	EvidenceCount int    `json:"evidence_count"`
}

// Receipt is a local, redacted summary of one requested evidence-backed run.
// Cost is execution-only in v1 because admission and review transports do not
// consistently expose actual provider usage.
type Receipt struct {
	Version            int                `json:"version"`
	RunID              string             `json:"run_id"`
	TaskID             string             `json:"task_id"`
	CreatedAt          time.Time          `json:"created_at"`
	TaskKind           string             `json:"task_kind"`
	Risk               string             `json:"risk"`
	Model              string             `json:"model"`
	Runtime            string             `json:"runtime,omitempty"`
	Outcome            Outcome            `json:"outcome"`
	Criteria           []CriterionReceipt `json:"criteria"`
	EvidenceCoverage   int                `json:"evidence_coverage"`
	EvidenceTotal      int                `json:"evidence_total"`
	ExecutionCostUSD   float64            `json:"execution_cost_usd,omitempty"`
	ExecutionCostKnown bool               `json:"execution_cost_known"`
	ExecutionLatencyMS int64              `json:"execution_latency_ms,omitempty"`
	LatencyKnown       bool               `json:"execution_latency_known"`
}

func ParseCriteria(data []byte) ([]string, error) {
	var manifest CriteriaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("criteria manifest: %w", err)
	}
	if manifest.Version != SchemaVersion {
		return nil, fmt.Errorf("criteria manifest: unsupported version %d", manifest.Version)
	}
	return validateCriteria(manifest.Criteria)
}

func ParseEvidence(data []byte, criteria []string) ([]Evidence, error) {
	var manifest EvidenceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("evidence manifest: %w", err)
	}
	if manifest.Version != SchemaVersion {
		return nil, fmt.Errorf("evidence manifest: unsupported version %d", manifest.Version)
	}
	if _, err := validateCriteria(criteria); err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		allowed[criterion] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Evidence))
	covered := make(map[string]struct{}, len(criteria))
	for index := range manifest.Evidence {
		evidence := &manifest.Evidence[index]
		evidence.ID = strings.TrimSpace(evidence.ID)
		evidence.Criterion = strings.TrimSpace(evidence.Criterion)
		evidence.Type = strings.TrimSpace(evidence.Type)
		rawSummary := strings.TrimSpace(evidence.Summary)
		if len(rawSummary) > maxSummaryBytes {
			return nil, fmt.Errorf("evidence manifest: evidence %q summary exceeds %d bytes", evidence.ID, maxSummaryBytes)
		}
		evidence.Summary = ledger.Redact(rawSummary)
		evidence.SHA256 = strings.ToLower(strings.TrimSpace(evidence.SHA256))
		if evidence.ID == "" || evidence.Criterion == "" || evidence.Type == "" || evidence.Summary == "" {
			return nil, fmt.Errorf("evidence manifest: evidence %d requires id, criterion, type, and summary", index+1)
		}
		if _, ok := seen[evidence.ID]; ok {
			return nil, fmt.Errorf("evidence manifest: duplicate evidence id %q", evidence.ID)
		}
		seen[evidence.ID] = struct{}{}
		if _, ok := allowed[evidence.Criterion]; !ok {
			return nil, fmt.Errorf("evidence manifest: evidence %q references unknown criterion", evidence.ID)
		}
		if evidence.SHA256 != "" && !validSHA256(evidence.SHA256) {
			return nil, fmt.Errorf("evidence manifest: evidence %q has invalid sha256", evidence.ID)
		}
		covered[evidence.Criterion] = struct{}{}
	}
	for _, criterion := range criteria {
		if _, ok := covered[criterion]; !ok {
			return nil, fmt.Errorf("evidence manifest: criterion %q has no evidence", criterion)
		}
	}
	return manifest.Evidence, nil
}

func validateCriteria(criteria []string) ([]string, error) {
	if len(criteria) == 0 {
		return nil, fmt.Errorf("criteria manifest: at least one criterion is required")
	}
	seen := make(map[string]struct{}, len(criteria))
	result := make([]string, len(criteria))
	for index, criterion := range criteria {
		criterion = strings.TrimSpace(criterion)
		if criterion == "" {
			return nil, fmt.Errorf("criteria manifest: criterion %d is empty", index+1)
		}
		if _, ok := seen[criterion]; ok {
			return nil, fmt.Errorf("criteria manifest: duplicate criterion %q", criterion)
		}
		seen[criterion] = struct{}{}
		result[index] = criterion
	}
	return result, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
