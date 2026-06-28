package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"golang.org/x/term"
)

func cmdExec(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "suppress routing pipeline — print model output only")
	dryRun := fs.Bool("dry-run", false, "print steps without executing")
	timeout := fs.Duration("timeout", 60*time.Second, "per-step timeout")
	onFailure := fs.String("on-failure", "", "abort-ask|abort|continue (default from config or abort-ask)")
	_ = fs.Parse(args)

	if len(fs.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "error: provide a plan file")
		fmt.Fprintln(os.Stderr, "usage: veto exec <plan.md> [--dry-run] [--quiet] [--on-failure abort-ask|abort|continue]")
		os.Exit(1)
	}
	planFile := fs.Args()[0]

	data, err := os.ReadFile(planFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read %s: %v\n", planFile, err)
		os.Exit(1)
	}

	failureMode := resolveOnFailure(*onFailure)

	setupLogger()

	reg, mgr, store, err := prepareRouting()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	sigCtx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()

	plan, err := loadOrConvertPlan(sigCtx, planFile, data, reg, mgr, *quiet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		_ = store.Save()
		os.Exit(1)
	}

	fmt.Printf("\n  Plan: %s (%d step(s))\n", plan.Title, len(plan.Steps))

	if *dryRun {
		fmt.Println()
		for i, s := range plan.Steps {
			task := s.Task
			if len(task) > 58 {
				task = task[:55] + "..."
			}
			fmt.Printf("  %2d  %-12s  %-6s  %s\n", i+1, s.Kind, s.Risk, task)
		}
		fmt.Println()
		return
	}

	var failed []int
	for i, step := range plan.Steps {
		n := i + 1
		stepCtx, cancel := context.WithTimeout(sigCtx, *timeout)

		render := NewRenderer(*quiet)
		render.PrintTaskHeader(step.Task, step.Kind, step.Risk, 0, false)

		spec := router.TaskSpec{
			ID:        taskHash(step.Task, step.Kind, step.Risk, 0),
			Kind:      router.TaskKind(step.Kind),
			Objective: step.Task,
			Risk:      router.Risk(step.Risk),
		}
		modelName, output, execErr := routeAndCapture(stepCtx, reg, mgr, render, spec)
		cancel()

		if execErr != nil {
			failed = append(failed, n)
			fmt.Fprintf(os.Stderr, "\n  Step %d failed: %v\n", n, execErr)
			if !handleStepFailure(failureMode, *quiet, n) {
				_ = store.Save()
				os.Exit(1)
			}
			continue
		}

		if !*quiet {
			fmt.Printf("\n  ── Running on %-10s %s\n\n", modelName, strings.Repeat("─", 35))
		}
		fmt.Println(output)
		if step.SuccessCriteria != "" && !*quiet {
			fmt.Printf("  ✓  %s\n", step.SuccessCriteria)
		} else if !*quiet {
			fmt.Printf("  ✓  Step %d done\n", n)
		}
	}

	_ = store.Save()

	if failureMode == "continue" && len(failed) > 0 {
		fmt.Printf("\n  Summary: %d ok, %d failed (steps: %s)\n",
			len(plan.Steps)-len(failed), len(failed), joinInts(failed))
		os.Exit(1)
	}
}

func loadOrConvertPlan(ctx context.Context, planFile string, data []byte, reg *providerRegistry, mgr *router.Manager, quiet bool) (*VetoPlan, error) {
	plan, parseErr := ParsePlan(data)
	var violations []string
	if parseErr == nil {
		violations = ValidatePlan(plan)
	}

	if parseErr == nil && len(violations) == 0 {
		return plan, nil
	}

	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "\n  Plan validation failed: %v\n", parseErr)
	} else {
		fmt.Fprintf(os.Stderr, "\n  Plan validation failed (%d issue(s)):\n", len(violations))
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "    • %s\n", v)
		}
	}

	if quiet {
		return nil, fmt.Errorf("plan is invalid; re-run without --quiet to convert interactively")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, fmt.Errorf("plan is invalid and stdin is not a terminal; fix the plan or run interactively to convert")
	}

	fmt.Printf("\n  Convert %s to veto plan spec? [y/N]: ", planFile)
	var answer string
	fmt.Scanln(&answer) //nolint:errcheck
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return nil, fmt.Errorf("conversion declined")
	}

	fmt.Println("  Converting...")
	converted, raw, err := convertPlan(ctx, reg, mgr, string(data))
	if err != nil {
		return nil, err
	}

	if convViolations := ValidatePlan(converted); len(convViolations) > 0 {
		fmt.Fprintln(os.Stderr, "\n  Converted plan still has issues:")
		for _, v := range convViolations {
			fmt.Fprintf(os.Stderr, "    • %s\n", v)
		}
		return nil, fmt.Errorf("converted plan is not valid")
	}

	savePath := convertedPlanPath(converted.Title)
	if err := os.MkdirAll(filepath.Dir(savePath), 0700); err != nil {
		return nil, fmt.Errorf("cannot create plans dir: %w", err)
	}
	if err := os.WriteFile(savePath, raw, 0600); err != nil {
		return nil, fmt.Errorf("cannot save converted plan: %w", err)
	}
	fmt.Printf("  Saved: %s\n  (original %s unchanged)\n", savePath, planFile)
	return converted, nil
}

func handleStepFailure(mode string, quiet bool, _ int) bool {
	switch mode {
	case "continue":
		return true
	case "abort":
		return false
	default: // "abort-ask"
		if quiet || !term.IsTerminal(int(os.Stdout.Fd())) {
			return false
		}
		fmt.Print("  Continue with remaining steps? [y/N]: ")
		var answer string
		fmt.Scanln(&answer) //nolint:errcheck
		return strings.ToLower(strings.TrimSpace(answer)) == "y"
	}
}

func resolveOnFailure(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	cfg := loadVetoConfig()
	if v := cfg["on_failure"]; v != "" {
		return v
	}
	return "abort-ask"
}

type vetoConfig map[string]string

func loadVetoConfig() vetoConfig {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".veto", "config.json"))
	if err != nil {
		return vetoConfig{}
	}
	var c vetoConfig
	_ = json.Unmarshal(data, &c)
	return c
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}
