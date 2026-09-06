package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/internal/controlplane"
)

type fakeNativeCommand struct{ ran bool }

func (c *fakeNativeCommand) Run() error            { c.ran = true; return nil }
func (c *fakeNativeCommand) SetStdin(_ io.Reader)  {}
func (c *fakeNativeCommand) SetStdout(_ io.Writer) {}
func (c *fakeNativeCommand) SetStderr(_ io.Writer) {}

func TestModelRendersNativeBillingUnknownAndTemporaryUnavailable(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{NoColor: true})
	model.snapshot.Providers = []controlplane.ProviderSnapshot{{Name: "Claude", Installed: true, Auth: "unknown", Billing: "unknown", Warning: "billing cannot be verified"}, {Name: "Codex", Installed: true, Auth: "authenticated", Billing: "api", Unavailable: true}}
	model.activeAction = "providers"
	view := model.renderProviders(140)
	for _, want := range []string{"PROVIDER HEALTH", "billing=unknown", "temporarily unavailable", "billing cannot be verified"} {
		if !strings.Contains(view, want) {
			t.Errorf("native status missing %q\n%s", want, view)
		}
	}
}

func TestModelReviewsNativeDecisionBeforeLaunch(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{NoColor: true})
	request := controlplane.ActionRequest{ActionID: "start", Arguments: map[string]string{"objective": "fix parser"}}
	command := &fakeNativeCommand{}
	updated, _ := model.Update(executionResultMsg{request: request, result: controlplane.ActionResult{ActionID: "start", Summary: "Proposal: claude / sonnet\nHistory: not used", Command: command}})
	model = updated.(*Model)
	if !model.confirmOpen || model.pendingNative == nil || model.running || command.ran {
		t.Fatalf("native review state = confirm:%v pending:%v running:%v ran:%v", model.confirmOpen, model.pendingNative != nil, model.running, command.ran)
	}
	if !strings.Contains(model.View().Content, "History: not used") {
		t.Fatal("decision explanation not rendered")
	}
}
