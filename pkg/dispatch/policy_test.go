package dispatch

import "testing"

func TestDecideAgentUsesDefaultAndNeverHistory(t *testing.T) {
	decision, err := Decide(Request{Mode: ModeChooseAgent, DefaultAgent: "claude", Kind: "code-change"}, []AgentStatus{
		{Name: "claude", Installed: true, Auth: AuthAuthenticated, Billing: BillingUnknown},
		{Name: "codex", Installed: true, Auth: AuthAuthenticated, Billing: BillingUnknown},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Agent != "claude" || decision.HistoricalInfluenced || len(decision.Unknown) == 0 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDecideAgentExcludesUnavailablePreferredAgent(t *testing.T) {
	decision, err := Decide(Request{Mode: ModeChooseAgent, Kind: "code-change"}, []AgentStatus{
		{Name: "claude", Installed: true, Auth: AuthAuthenticated},
		{Name: "codex", Installed: true, Auth: AuthAuthenticated, Unavailable: true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Agent != "claude" || len(decision.AlternativesExcluded) != 1 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDecideModelIsDeterministicAndAgentFixed(t *testing.T) {
	decision, err := Decide(Request{Mode: ModeChooseModel, Agent: "claude", Kind: "debug", Risk: "high"}, []AgentStatus{{Name: "claude", Installed: true, Auth: AuthUnknown, Billing: BillingUnknown}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Agent != "claude" || decision.Model != "opus" || decision.HistoricalInfluenced {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestManualSelectionDoesNotRoute(t *testing.T) {
	decision, err := Decide(Request{Mode: ModeManual, Agent: "codex", Model: "gpt-5-codex"}, []AgentStatus{{Name: "codex", Installed: true, Auth: AuthAuthenticated}}, nil)
	if err != nil || decision.Agent != "codex" || decision.Model != "gpt-5-codex" || decision.HistoricalInfluenced {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestChooseModelAllowsExplicitCodexModel(t *testing.T) {
	decision, err := Decide(Request{Mode: ModeChooseModel, Agent: "codex", Model: "gpt-5-codex"}, []AgentStatus{{Name: "codex", Installed: true, Auth: AuthAuthenticated}}, nil)
	if err != nil || decision.Agent != "codex" || decision.Model != "gpt-5-codex" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}
