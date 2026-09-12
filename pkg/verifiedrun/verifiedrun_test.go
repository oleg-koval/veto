package verifiedrun

import (
	"strings"
	"testing"
)

func TestParseEvidenceRequiresBoundedCoverage(t *testing.T) {
	criteria, err := ParseCriteria([]byte(`{"version":1,"criteria":["tests pass","p95 improves"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseEvidence([]byte(`{"version":1,"evidence":[{"id":"tests","criterion":"tests pass","type":"test","summary":"ok"}]}`), criteria)
	if err == nil || !strings.Contains(err.Error(), "p95 improves") {
		t.Fatalf("error = %v, want missing criterion", err)
	}
}

func TestParseEvidenceRejectsUnknownCriterionAndDigest(t *testing.T) {
	criteria := []string{"tests pass"}
	_, err := ParseEvidence([]byte(`{"version":1,"evidence":[{"id":"tests","criterion":"unknown","type":"test","summary":"ok"}]}`), criteria)
	if err == nil || !strings.Contains(err.Error(), "unknown criterion") {
		t.Fatalf("error = %v", err)
	}
	_, err = ParseEvidence([]byte(`{"version":1,"evidence":[{"id":"tests","criterion":"tests pass","type":"test","summary":"ok","sha256":"bad"}]}`), criteria)
	if err == nil || !strings.Contains(err.Error(), "invalid sha256") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseEvidenceRedactsSummaryBeforeReview(t *testing.T) {
	evidence, err := ParseEvidence([]byte(`{"version":1,"evidence":[{"id":"tests","criterion":"tests pass","type":"test","summary":"token=sk-abcdefghi"}]}`), []string{"tests pass"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evidence[0].Summary, "sk-abcdefghi") {
		t.Fatalf("summary was not redacted: %q", evidence[0].Summary)
	}
}

func TestParseEvidenceMatchesNormalizedCriteria(t *testing.T) {
	evidence, err := ParseEvidence([]byte(`{"version":1,"evidence":[{"id":"tests","criterion":"tests pass","type":"test","summary":"ok"}]}`), []string{" tests pass "})
	if err != nil {
		t.Fatal(err)
	}
	if evidence[0].Criterion != "tests pass" {
		t.Fatalf("criterion = %q, want normalized criterion", evidence[0].Criterion)
	}
}
