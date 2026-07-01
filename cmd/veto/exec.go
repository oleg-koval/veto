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
	var allOutputs []string
	var allCriteria []string

	for i, step := range plan.Steps {
		n := i + 1
		stepCtx, cancel := context.WithTimeout(sigCtx, *timeout)

		render := NewRenderer(*quiet)
		stepComplexity := router.InferComplexity(step.Task, router.TaskKind(step.Kind))
		render.PrintTaskHeader(step.Task, step.Kind, step.Risk, string(stepComplexity), 0, false)

		criteria := splitCriteria(step.SuccessCriteria)
		allCriteria = append(allCriteria, criteria...)

		spec := router.TaskSpec{
			ID:              taskHash(step.Task, step.Kind, step.Risk, 0),
			Kind:            router.TaskKind(step.Kind),
			Complexity:      stepComplexity,
			Objective:       step.Task,
			Risk:            router.Risk(step.Risk),
			SuccessCriteria: criteria,
		}

		skillNames, skillBodies := resolveSkills(stepCtx, reg, mgr, spec)
		render.PrintSkills(skillNames)

		modelName, output, execErr := routeAndCapture(stepCtx, reg, mgr, render, spec, skillBodies)
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

		allOutputs = append(allOutputs, output)

		if !*quiet {
			fmt.Printf("\n  ── Running on %-10s %s\n\n", modelName, strings.Repeat("─", 35))
		}
		fmt.Println(output)

		// per-step acceptance-criteria review
		if len(criteria) > 0 {
			if result, ok := reviewOutput(sigCtx, reg, mgr, spec, output, modelName); ok {
				render.PrintReview(result)
				if !result.Passed {
					failed = append(failed, n)
					fmt.Fprintf(os.Stderr, "\n  Step %d review failed: criteria not met\n", n)
					if !handleStepFailure(failureMode, *quiet, n) {
						_ = store.Save()
						os.Exit(1)
					}
					continue
				}
			}
		} else if step.SuccessCriteria != "" && !*quiet {
			fmt.Printf("  ✓  %s\n", step.SuccessCriteria)
		} else if !*quiet {
			fmt.Printf("  ✓  Step %d done\n", n)
		}
	}

	_ = store.Save()

	// final integrator pass: one cross-step review verifying no regression across all steps
	if len(allCriteria) > 0 && len(allOutputs) > 0 && len(failed) == 0 {
		allCriteria = append(allCriteria, "no step undid another step's work (no regression introduced)")
		finalSpec := router.TaskSpec{
			ID:              taskHash(fmt.Sprintf("%s|steps=%d", plan.Title, len(plan.Steps)), "review", "medium", 0),
			Kind:            router.KindReview,
			Objective:       fmt.Sprintf("Plan: %s\n\nAll %d step(s) completed.", plan.Title, len(plan.Steps)),
			Risk:            router.RiskMedium,
			SuccessCriteria: allCriteria,
		}
		combinedOutput := strings.Join(allOutputs, "\n\n---\n\n")
		finalRender := NewRenderer(*quiet)
		if !*quiet {
			fmt.Println("\n  ── Final review " + strings.Repeat("─", 38))
		}
		// "" = no single executor to exclude; the plan used multiple models
		if result, ok := reviewOutput(sigCtx, reg, mgr, finalSpec, combinedOutput, ""); ok {
			finalRender.PrintReview(result)
			if !result.Passed {
				fmt.Fprintln(os.Stderr, "  Final review failed: acceptance criteria not fully met.")
				os.Exit(1)
			}
		}
	}

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

// splitCriteria splits a raw SuccessCriteria string (from plan YAML) into a slice.
// Splits on newlines and semicolons; trims whitespace; drops empties.
func splitCriteria(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' })
	var out []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
