package shadow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const maxEventBytes = 1024 * 1024

var forbiddenEvidenceFields = map[string]bool{
	"objective": true, "admission_objective": true, "constraints": true,
	"credential": true, "credentials": true, "api_key": true, "authorization": true,
	"secret": true, "response": true, "response_body": true, "raw_response": true,
	"provider_response_body": true, "detail": true, "explanation": true,
	"free_form_explanation": true, "filesystem_path": true, "path": true,
	"account_id": true, "account_identifier": true,
}

// Append writes one validated event as one JSON line.
func Append(w io.Writer, event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if err := json.NewEncoder(w).Encode(event); err != nil {
		return fmt.Errorf("shadow evidence append: %w", err)
	}
	return nil
}

// Load reads and validates every nonblank JSONL record. Unknown JSON fields are
// ignored for additive v1 compatibility; unsupported versions and event kinds
// fail closed because their semantics are not known.
func Load(r io.Reader) ([]Event, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)
	var events []Event
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		if err := rejectForbiddenFields(raw); err != nil {
			return nil, fmt.Errorf("shadow evidence load line %d: %w", line, err)
		}
		var event Event
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(&event); err != nil {
			return nil, fmt.Errorf("shadow evidence load line %d: %w", line, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("multiple JSON values")
			}
			return nil, fmt.Errorf("shadow evidence load line %d: %w", line, err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("shadow evidence load line %d: %w", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("shadow evidence load: %w", err)
	}
	return events, nil
}

// rejectForbiddenFields recursively rejects sensitive field names, including inside unknown additive fields.
func rejectForbiddenFields(raw []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var visit func(any) error
	visit = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
				if forbiddenEvidenceFields[normalized] {
					return fmt.Errorf("shadow evidence: forbidden field %q", key)
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}

// RouteRecord is a materialized comparison with its latest candidate labels.
type RouteRecord struct {
	Comparison RouteComparison
	Labels     map[string]ExecutionLabel
}

// Dataset is the deterministic materialized view of an append-only stream.
type Dataset struct {
	Routes []RouteRecord
}

// Materialize joins labels to comparisons. Comparisons retain stream order;
// repeated labels replace only the same route/candidate label in the view.
func Materialize(events []Event) (Dataset, error) {
	positions := make(map[string]int)
	var dataset Dataset
	for index, event := range events {
		if err := event.Validate(); err != nil {
			return Dataset{}, fmt.Errorf("shadow evidence event %d: %w", index+1, err)
		}
		switch event.Type {
		case EventRouteComparison:
			routeID := event.Comparison.RouteID
			if _, exists := positions[routeID]; exists {
				return Dataset{}, fmt.Errorf("shadow evidence: duplicate comparison for route %q", routeID)
			}
			positions[routeID] = len(dataset.Routes)
			dataset.Routes = append(dataset.Routes, RouteRecord{Comparison: *event.Comparison, Labels: make(map[string]ExecutionLabel)})
		case EventExecutionLabel:
			position, exists := positions[event.Label.RouteID]
			if !exists {
				return Dataset{}, fmt.Errorf("shadow evidence: label references unknown route %q", event.Label.RouteID)
			}
			record := &dataset.Routes[position]
			if !offered(record.Comparison.Candidates, event.Label.Candidate) {
				return Dataset{}, fmt.Errorf("shadow evidence: label references unknown candidate %q", event.Label.Candidate)
			}
			record.Labels[event.Label.Candidate] = *event.Label
		}
	}
	return dataset, nil
}

// offered reports whether the exact candidate key appears in the shortlist.
func offered(candidates []Candidate, key string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.Key, key) && candidate.Key == key {
			return true
		}
	}
	return false
}
