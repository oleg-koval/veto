package router

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskRoleConstants(t *testing.T) {
	roles := []TaskRole{
		RoleUnspecified, RoleOrchestrator, RoleExplorer, RoleWorker,
		RoleTester, RoleReviewer, RoleResearcher,
	}
	want := []string{
		"unspecified", "orchestrator", "explorer", "worker",
		"tester", "reviewer", "researcher",
	}

	for i, role := range roles {
		assert.Equal(t, want[i], string(role))
	}
}

func TestNormalizeTaskRole(t *testing.T) {
	tests := []struct {
		name string
		role TaskRole
		want TaskRole
	}{
		{name: "empty", want: RoleUnspecified},
		{name: "whitespace", role: "  ", want: RoleUnspecified},
		{name: "unspecified", role: RoleUnspecified, want: RoleUnspecified},
		{name: "orchestrator", role: RoleOrchestrator, want: RoleOrchestrator},
		{name: "explorer", role: RoleExplorer, want: RoleExplorer},
		{name: "worker", role: RoleWorker, want: RoleWorker},
		{name: "tester", role: RoleTester, want: RoleTester},
		{name: "reviewer", role: RoleReviewer, want: RoleReviewer},
		{name: "researcher", role: RoleResearcher, want: RoleResearcher},
		{name: "case and whitespace", role: " Reviewer ", want: RoleReviewer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeTaskRole(tt.role)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeTaskRoleRejectsUnknownRole(t *testing.T) {
	role, err := NormalizeTaskRole("manager")

	assert.Empty(t, role)
	assert.ErrorContains(t, err, "supported roles")
	assert.ErrorContains(t, err, "orchestrator")
}

func TestTaskSpecRoleJSONCompatibility(t *testing.T) {
	legacy := []byte(`{"ID":"legacy","Kind":"review"}`)
	var decoded TaskSpec
	assert.NoError(t, json.Unmarshal(legacy, &decoded))
	assert.Equal(t, "legacy", decoded.ID)
	assert.Equal(t, KindReview, decoded.Kind)

	normalized, err := NormalizeTaskRole(decoded.Role)
	assert.NoError(t, err)
	assert.Equal(t, RoleUnspecified, normalized)

	withoutRole, err := json.Marshal(TaskSpec{ID: "legacy"})
	assert.NoError(t, err)
	assert.NotContains(t, string(withoutRole), `"role"`)

	withRole, err := json.Marshal(TaskSpec{ID: "review", Role: RoleReviewer})
	assert.NoError(t, err)
	assert.Contains(t, string(withRole), `"role":"reviewer"`)
}

func TestTaskSpecRoleJSONDecodeNormalizesAndValidates(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TaskRole
		wantErr string
	}{
		{name: "empty", input: `{"role":""}`, want: RoleUnspecified},
		{name: "null", input: `{"role":null}`, want: RoleUnspecified},
		{name: "case and whitespace", input: `{"role":" Reviewer "}`, want: RoleReviewer},
		{name: "unknown", input: `{"role":"manager"}`, wantErr: "supported roles"},
		{name: "non-string", input: `{"role":1}`, wantErr: "decode task role"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var decoded TaskSpec
			err := json.Unmarshal([]byte(tt.input), &decoded)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, decoded.Role)
		})
	}
}

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
	assert.Empty(t, ts.Role)
	assert.Zero(t, ts.MaxCostUSD)
}

func TestAdmissionDecisionDefaults(t *testing.T) {
	var d AdmissionDecision
	assert.False(t, d.Accept)
	assert.Zero(t, d.Confidence)
	assert.Nil(t, d.ReasonCodes)
}
