package eval

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCorpus() Corpus {
	return Corpus{
		Version: 1,
		Models: []ModelFixture{
			{Name: "cheap", Tier: "small", MaxContextTokens: 10000, CostPer1kInputUSD: 0.001, CostPer1kOutputUSD: 0.001, Strengths: []router.TaskKind{router.KindCodeChange}},
			{Name: "strong", Tier: "large", MaxContextTokens: 10000, CostPer1kInputUSD: 0.01, CostPer1kOutputUSD: 0.01, Strengths: []router.TaskKind{router.KindCodeChange}},
		},
		Tasks: []TaskFixture{
			{
				ID: "task-1", Kind: router.KindCodeChange, Complexity: router.ComplexitySimple,
				Outcomes: map[string]Outcome{
					"cheap":  {Accepted: true, Success: true, Confidence: 0.8, ConfidenceKnown: true, Score: 0.4, ScoreKnown: true, CostUSD: 0.001, CostKnown: true, LatencyMs: 10, LatencyKnown: true},
					"strong": {Accepted: true, Success: true, Confidence: 0.95, ConfidenceKnown: true, Score: 0.9, ScoreKnown: true, CostUSD: 0.01, CostKnown: true, LatencyMs: 100, LatencyKnown: true},
				},
			},
		},
	}
}

func TestEvaluateComparesAllPoliciesDeterministically(t *testing.T) {
	report := Evaluate(testCorpus())

	require.Len(t, report.Policies, 4)
	assert.Equal(t, []string{"cheapest", "strongest", "static", "adaptive"}, policyNames(report))
	assert.Equal(t, "cheap", report.Policies[0].Selections[0].Model)
	assert.Equal(t, "strong", report.Policies[1].Selections[0].Model)
	assert.Equal(t, "cheap", report.Policies[2].Selections[0].Model)
	assert.Equal(t, "cheap", report.Policies[3].Selections[0].Model)
	assert.Equal(t, 1, report.Policies[0].Metrics.Successes)
	assert.InDelta(t, 0.4, report.Policies[0].Metrics.AverageScore, 0.001)
	assert.InDelta(t, 0.001, report.Policies[0].Metrics.AverageCostUSD, 0.00001)
	assert.Equal(t, 1, report.Policies[0].Metrics.ConfidenceSamples)
	assert.Greater(t, report.Policies[0].Metrics.MeanConfidenceError, 0.0)
}

func TestLoadRejectsInvalidCorpus(t *testing.T) {
	_, err := Load(bytes.NewBufferString(`{"version":1,"models":[],"tasks":[]}`))
	require.Error(t, err)
}

func TestReportJSONIsMachineReadable(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, WriteJSON(&out, Evaluate(testCorpus())))
	var decoded Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, []string{"cheapest", "strongest", "static", "adaptive"}, policyNames(decoded))
}

func TestRoutingCorpusReplaysOffline(t *testing.T) {
	corpus, err := LoadFile("testdata/routing_corpus.json")
	require.NoError(t, err)
	report := Evaluate(corpus)

	assert.Len(t, corpus.Tasks, 6)
	assert.Equal(t, 6, report.Policies[0].Metrics.Tasks)
	assert.Greater(t, report.Policies[0].Metrics.AdmissionAttempts, 0)
	assert.Greater(t, report.Policies[0].Metrics.P95CostUSD, 0.0)
	assert.Equal(t, 4, report.Policies[2].Metrics.Successes, "static policy is the baseline")
	assert.Equal(t, 5, report.Policies[3].Metrics.Successes, "adaptive replay should learn from the failed cheap attempt")
	assert.Equal(t, "balanced-mid", report.Policies[3].Selections[4].Model)
	assert.Equal(t, 1, report.Policies[0].Metrics.BudgetViolations)
	assert.Greater(t, report.Policies[3].Metrics.ConfidenceSamples, 0)
}

func policyNames(report Report) []string {
	names := make([]string, len(report.Policies))
	for i, p := range report.Policies {
		names[i] = p.Name
	}
	return names
}
