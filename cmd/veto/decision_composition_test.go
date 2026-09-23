package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/require"
)

// Exercise real composition roots with a local CLI fixture and isolated HOME.
// No engine setter is used: these paths must inherit NewManager's default.
func TestDecisionCompositionProcess(t *testing.T) {
	mode := os.Getenv("VETO_TEST_DECISION_PATH")
	if mode == "" {
		return
	}
	switch mode {
	case "normal", "quiet", "json":
		args := []string{"--no-resume", "--kind", "summarize"}
		if mode != "normal" {
			args = append(args, "--"+mode)
		}
		cmdRoute(append(args, "summarize this"))
	case "run":
		cmdRun([]string{"--quiet", "--no-feedback", "--kind", "summarize", "summarize this"})
	case "plan":
		cmdExec([]string{"--quiet", "--no-feedback", os.Getenv("VETO_TEST_PLAN")})
	case "review":
		setupLogger()
		reg, mgr, _, err := prepareRouting()
		require.NoError(t, err)
		result, err := reviewOutput(context.Background(), reg, mgr, router.TaskSpec{ID: "review", Kind: router.KindSummarize, SuccessCriteria: []string{"correct"}}, "done", "")
		require.NoError(t, err)
		require.True(t, result.Passed)
	case "tui":
		setupLogger()
		reg, mgr, _, err := prepareTUIRouting()
		require.NoError(t, err)
		service := application.NewControlService(newApplicationRunner(reg, mgr), mgr)
		service.SetRouteEventRecorder(func(e router.ProgressEvent) { logEvent("tui", "summarize", "low", e) })
		updates := service.Subscribe(context.Background())
		_, err = service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{"objective": "summarize this", "kind": "summarize"}})
		require.NoError(t, err)
		found := false
		for len(updates) > 0 {
			e := <-updates
			if e.Kind == "decision.completed" {
				require.NotNil(t, e.Decision)
				require.Equal(t, router.DecisionModeSequentialAdmission, e.Decision.Mode)
				found = true
			}
		}
		require.True(t, found)
	}
	closeLogger()
	os.Exit(0) // Preserve command stdout, without the test harness PASS line.
}

func TestDecisionCompositionAndRoutingOutput(t *testing.T) {
	for _, mode := range []string{"normal", "quiet", "json", "run", "plan", "review", "tui"} {
		t.Run(mode, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".veto"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".veto", "credentials.json"), []byte(`{"CLAUDE_SUBSCRIPTION":"true"}`), 0600))
			script := `#!/bin/sh
case "$*" in
 *"--output-format json"*) printf '%s\n' '{"is_error":false,"structured_output":{"accept":true,"confidence":0.95,"reason_codes":[],"estimated_tokens":100,"estimated_cost_usd":0}}' ;;
 *) printf '%s\n' '{"passed":true,"score":1,"criteria":[{"criterion":"correct","met":true,"note":"ok"}]}' ;;
esac
`
			require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700))
			plan := filepath.Join(home, "plan.md")
			require.NoError(t, os.WriteFile(plan, []byte("---\ntitle: Fixture\nversion: 1\nsteps:\n  - task: summarize this\n    kind: summarize\n    risk: low\n---\n"), 0600))
			cmd := exec.Command(os.Args[0], "-test.run=^TestDecisionCompositionProcess$")
			cmd.Env = append(cleanRunTestEnv(os.Environ()), "VETO_TEST_DECISION_PATH="+mode, "VETO_TEST_PLAN="+plan, "HOME="+home, "PATH="+bin+":/usr/bin:/bin")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			require.NotContains(t, string(out), "decision.")
			paths, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
			require.NoError(t, err)
			require.Len(t, paths, 1)
			data, err := os.ReadFile(paths[0])
			require.NoError(t, err)
			events, corrupt, err := ledger.Read(strings.NewReader(string(data)))
			require.NoError(t, err)
			require.Zero(t, corrupt)
			var boundaries []ledger.Event
			for _, e := range events {
				if e.Decision != nil {
					boundaries = append(boundaries, e)
				}
			}
			require.Len(t, boundaries, 2)
			require.Equal(t, ledger.EventDecisionStarted, boundaries[0].Type)
			require.Equal(t, ledger.EventDecisionCompleted, boundaries[1].Type)
			require.Equal(t, router.DecisionModeSequentialAdmission, boundaries[1].Decision.Mode)
			model := boundaries[1].Decision.SelectedModel
			require.NotEmpty(t, model)
			switch mode {
			case "quiet":
				require.Equal(t, model+"\n", string(out))
			case "normal":
				require.Contains(t, string(out), model)
				require.Contains(t, string(out), "Asking models")
			case "json":
				var got map[string]any
				require.NoError(t, json.Unmarshal(out, &got))
				require.Equal(t, model, got["model"])
				require.Equal(t, 0.95, got["confidence"])
				require.NotContains(t, got, "decision")
				require.NotContains(t, got, "contract_version")
			}
		})
	}
}

func TestDecisionEventsPreserveRendererOutput(t *testing.T) {
	legacy := []router.ProgressEvent{
		{Kind: router.EventFilterPass, Model: "fixture"},
		{Kind: router.EventShortlist, Detail: "fixture"},
		{Kind: router.EventAskStart, Model: "fixture"},
		{Kind: router.EventAskAccept, Model: "fixture", Confidence: 0.95},
	}
	for _, quiet := range []bool{false, true} {
		render := func(withBoundaries bool) string {
			file, err := os.CreateTemp(t.TempDir(), "stdout")
			require.NoError(t, err)
			defer file.Close()
			previous := os.Stdout
			os.Stdout = file
			defer func() { os.Stdout = previous }()
			r := NewRenderer(quiet)
			for i, e := range legacy {
				if withBoundaries && i == 2 {
					r.OnEvent(router.ProgressEvent{Kind: router.EventDecisionStarted})
				}
				r.OnEvent(e)
			}
			if withBoundaries {
				r.OnEvent(router.ProgressEvent{Kind: router.EventDecisionCompleted})
				r.OnEvent(router.ProgressEvent{Kind: router.EventDecisionError})
			}
			data, err := os.ReadFile(file.Name())
			require.NoError(t, err)
			return string(data)
		}
		baseline := render(false)
		require.Equal(t, baseline, render(true))
		if quiet {
			require.Empty(t, baseline)
		} else {
			require.Contains(t, baseline, "accepted")
		}
	}
}
