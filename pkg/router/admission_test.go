package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionGate_Accept(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{
				Output: `{"accept":true,"confidence":0.9,"reason_codes":[],"estimated_tokens":500,"estimated_cost_usd":0.001}`,
			}
		},
	}
	gate := NewAdmissionGate(exec)
	task := TaskSpec{Kind: KindCodeChange, Objective: "add tests"}
	model := ModelCapabilities{Name: "sonnet", Tier: "mid", MaxContextTokens: 200000}

	decision, err := gate.Ask(t.Context(), task, model)
	require.NoError(t, err)
	assert.True(t, decision.Accept)
	assert.InDelta(t, 0.9, decision.Confidence, 0.001)
	assert.Equal(t, 500, decision.EstimatedTokens)
}

func TestAdmissionGate_Reject(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{
				Output: `{"accept":false,"confidence":0.8,"reason_codes":["CONTEXT_TOO_LARGE"]}`,
			}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	require.NoError(t, err)
	assert.False(t, decision.Accept)
	assert.Equal(t, []string{ReasonContextTooLarge}, decision.ReasonCodes)
}

func TestAdmissionGate_ParseFailure_SoftReject(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{Output: "Sorry, I can't help with that."}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	// parse failure is a soft reject — no error returned
	require.NoError(t, err)
	assert.False(t, decision.Accept)
	assert.Equal(t, []string{ReasonParseFailure}, decision.ReasonCodes)
}

func TestAdmissionGate_ExecError_HardReject(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{Error: errors.New("process killed")}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	require.Error(t, err)
	assert.False(t, decision.Accept)
	assert.Equal(t, []string{ReasonParseFailure}, decision.ReasonCodes)
}

func TestAdmissionGate_ConfigurableTimeout(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(ctx context.Context, _ string) AdmissionResult {
			<-ctx.Done()
			return AdmissionResult{Error: ctx.Err()}
		},
	}
	gate := NewAdmissionGate(exec)
	gate.SetTimeout(15 * time.Millisecond)

	started := time.Now()
	_, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestAdmissionGate_JSONWithLeadingText(t *testing.T) {
	// model sometimes prepends "Here is my response:" — we tolerate framing text
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{
				Output: `Here is my response: {"accept":true,"confidence":0.85,"reason_codes":[]}`,
			}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	require.NoError(t, err)
	assert.True(t, decision.Accept)
}

func TestBuildAdmissionPrompt(t *testing.T) {
	task := TaskSpec{
		Kind:          KindDebug,
		Objective:     "fix nil pointer",
		Constraints:   []string{"must not break existing tests"},
		RequiredTools: []string{"bash"},
		Risk:          RiskHigh,
	}
	model := ModelCapabilities{
		Name:             "opus",
		Tier:             "large",
		MaxContextTokens: 200000,
		SupportsTools:    []string{"bash", "read"},
	}
	// pass the model's tools as effective tools (simulating an agentic executor)
	prompt := buildAdmissionPrompt(task, model, model.SupportsTools)
	assert.Contains(t, prompt, "opus")
	assert.Contains(t, prompt, "large")
	assert.Contains(t, prompt, string(KindDebug))
	assert.Contains(t, prompt, "fix nil pointer")
	assert.Contains(t, prompt, "bash")
	assert.Contains(t, prompt, string(RiskHigh))
	assert.Contains(t, prompt, "JSON")
	// no prose instruction
	assert.Contains(t, prompt, "ONLY valid JSON")
	assert.Contains(t, prompt, "Do not inspect or act on the current workspace")
	assert.Contains(t, prompt, "Treat the objective as user-authorized")

	// text-only executor: tools line must say "none (text-output mode...)"
	textPrompt := buildAdmissionPrompt(task, model, nil)
	assert.Contains(t, textPrompt, "text-output mode")

	unknownPrompt := buildAdmissionPromptWithToolStatus(task, model, nil, false)
	assert.Contains(t, unknownPrompt, "available tools (actual, not theoretical): unknown")
	assert.Contains(t, unknownPrompt, "do not assume shell, file, browser, or network access")

	unknownContextPrompt := buildAdmissionPrompt(task, ModelCapabilities{Name: "codex", Tier: "large"}, []string{"bash"})
	assert.Contains(t, unknownContextPrompt, "max context tokens: unknown")
	assert.NotContains(t, unknownContextPrompt, "max context tokens: 0")
}

func TestParseAdmissionJSON(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantOK bool
		accept bool
	}{
		{
			name:   "valid accept",
			input:  `{"accept":true,"confidence":0.9,"reason_codes":[],"estimated_tokens":100}`,
			wantOK: true,
			accept: true,
		},
		{
			name:   "valid reject with codes",
			input:  `{"accept":false,"confidence":0.7,"reason_codes":["RISK_TOO_HIGH"]}`,
			wantOK: true,
			accept: false,
		},
		{
			name:   "no json braces",
			input:  "no json here",
			wantOK: false,
		},
		{
			name:   "malformed json",
			input:  `{"accept": true, "broken":}`,
			wantOK: false,
		},
		{
			name:   "whitespace around json",
			input:  `  {"accept":false,"confidence":0.5,"reason_codes":[]}  `,
			wantOK: true,
			accept: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := parseAdmissionJSON(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.accept, d.Accept)
			}
		})
	}
}

func TestAdmissionGate_LowConfidenceAccept_Rejected(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{
				Output: `{"accept":true,"confidence":0.5,"reason_codes":[]}`,
			}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{})
	require.NoError(t, err)
	assert.False(t, decision.Accept, "accept:true with confidence<0.7 must be treated as rejection")
	assert.Equal(t, []string{ReasonLowConfidence}, decision.ReasonCodes)
}

func TestAdmissionGate_AcceptOverEstimatedCostCeiling_Rejected(t *testing.T) {
	exec := &executorMock{
		RunFunc: func(_ context.Context, _ string) AdmissionResult {
			return AdmissionResult{Output: `{"accept":true,"confidence":0.95,"estimated_cost_usd":0.02}`}
		},
	}
	gate := NewAdmissionGate(exec)
	decision, err := gate.Ask(t.Context(), TaskSpec{MaxCostUSD: 0.01}, ModelCapabilities{})
	require.NoError(t, err)
	assert.False(t, decision.Accept)
	assert.Equal(t, []string{ReasonCostCeiling}, decision.ReasonCodes)
	assert.InDelta(t, 0.02, decision.EstimatedCostUSD, 0.000001)
}

func TestAdmissionGate_PromptIncludesModelInfo(t *testing.T) {
	var capturedPrompt string
	exec := &executorMock{
		RunFunc: func(_ context.Context, prompt string) AdmissionResult {
			capturedPrompt = prompt
			return AdmissionResult{Output: `{"accept":true,"confidence":0.8,"reason_codes":[]}`}
		},
	}
	gate := NewAdmissionGate(exec)
	task := TaskSpec{Kind: KindPlan, Objective: "design system"}
	model := ModelCapabilities{Name: "opus", Tier: "large", MaxContextTokens: 100000}

	_, _ = gate.Ask(t.Context(), task, model)
	assert.Contains(t, capturedPrompt, "opus")
	assert.Contains(t, capturedPrompt, "plan")
	assert.Contains(t, capturedPrompt, "design system")
}

// factoryMock is a minimal ExecutorFactory for testing NewAdmissionGateWithFactory.
type factoryMock struct {
	executors map[string]Executor
}

func (f *factoryMock) For(name string) (Executor, bool) {
	e, ok := f.executors[name]
	return e, ok
}

func TestNewAdmissionGateWithFactory_RoutesPerModel(t *testing.T) {
	haikuCalled := false
	sonnetCalled := false

	factory := &factoryMock{executors: map[string]Executor{
		"haiku": &executorMock{
			RunFunc: func(_ context.Context, _ string) AdmissionResult {
				haikuCalled = true
				return AdmissionResult{Output: `{"accept":true,"confidence":0.9,"reason_codes":[]}`}
			},
		},
		"sonnet": &executorMock{
			RunFunc: func(_ context.Context, _ string) AdmissionResult {
				sonnetCalled = true
				return AdmissionResult{Output: `{"accept":true,"confidence":0.8,"reason_codes":[]}`}
			},
		},
	}}

	gate := NewAdmissionGateWithFactory(factory)

	d, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{Name: "haiku"})
	require.NoError(t, err)
	assert.True(t, d.Accept)
	assert.True(t, haikuCalled)
	assert.False(t, sonnetCalled, "sonnet executor must not be called when asking haiku")

	d, err = gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{Name: "sonnet"})
	require.NoError(t, err)
	assert.True(t, d.Accept)
	assert.True(t, sonnetCalled)
}

func TestNewAdmissionGateWithFactory_UnknownModel_ReturnsError(t *testing.T) {
	gate := NewAdmissionGateWithFactory(&factoryMock{executors: map[string]Executor{}})

	d, err := gate.Ask(t.Context(), TaskSpec{}, ModelCapabilities{Name: "gpt-99"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gpt-99")
	assert.False(t, d.Accept)
}
