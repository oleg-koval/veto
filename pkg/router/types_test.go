package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskKindConstants(t *testing.T) {
	kinds := []TaskKind{
		KindExtract, KindSummarize, KindCodeChange,
		KindDebug, KindPlan, KindReview, KindRefactor,
	}
	for _, k := range kinds {
		assert.NotEmpty(t, string(k))
	}
}

func TestRiskConstants(t *testing.T) {
	risks := []Risk{RiskLow, RiskMedium, RiskHigh}
	for _, r := range risks {
		assert.NotEmpty(t, string(r))
	}
}

func TestReasonCodeConstants(t *testing.T) {
	codes := []string{
		ReasonMissingTool, ReasonContextTooLarge, ReasonCostCeiling,
		ReasonWeakKind, ReasonRiskTooHigh, ReasonParseFailure,
	}
	for _, c := range codes {
		assert.NotEmpty(t, c)
	}
}

func TestTaskSpecZeroValue(t *testing.T) {
	var ts TaskSpec
	assert.Empty(t, ts.ID)
	assert.Empty(t, ts.Kind)
	assert.Zero(t, ts.MaxCostUSD)
}

func TestAdmissionDecisionDefaults(t *testing.T) {
	var d AdmissionDecision
	assert.False(t, d.Accept)
	assert.Zero(t, d.Confidence)
	assert.Nil(t, d.ReasonCodes)
}
