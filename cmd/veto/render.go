package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"golang.org/x/term"
)

type filterEntry struct {
	model  string
	pass   bool
	reason string
}

// Renderer displays the routing pipeline in the terminal.
// Set quiet=true or pipe stdout to suppress animation.
type Renderer struct {
	quiet         bool
	isTTY         bool
	filterBuf     []filterEntry // buffered until ask phase, then flushed once
	filterFlushed bool
	askHeader     bool // printed once before first ask event
	mu            sync.Mutex
	spin          *spinner
	spinModel     string // model name currently showing in spinner
	askCount      int    // in-flight ask goroutines (fan-out)
}

// NewRenderer creates a renderer. Animation is disabled when stdout is not a TTY or quiet=true.
func NewRenderer(quiet bool) *Renderer {
	return &Renderer{
		quiet: quiet,
		isTTY: term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// color wraps s in ANSI escape codes when stdout is a TTY.
func (r *Renderer) color(code, s string) string {
	if !r.isTTY {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// OnEvent is the event handler — wire it to Manager.OnEvent.
func (r *Renderer) OnEvent(e router.ProgressEvent) {
	if r.quiet {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	switch e.Kind {
	case router.EventFilterPass:
		r.filterBuf = append(r.filterBuf, filterEntry{model: e.Model, pass: true})

	case router.EventFilterFail:
		reason := ""
		if len(e.Reasons) > 0 {
			reason = e.Reasons[0]
		}
		r.filterBuf = append(r.filterBuf, filterEntry{model: e.Model, pass: false, reason: reason})

	case router.EventAskStart:
		r.flushFilters()
		if !r.askHeader {
			fmt.Println()
			fmt.Println("  ── Asking models " + strings.Repeat("─", 37))
			fmt.Println()
			r.askHeader = true
		}
		r.askCount++
		if r.isTTY && r.askCount == 1 {
			// First (or only) ask: animate a spinner.
			r.spinModel = e.Model
			r.spin = startSpinner(e.Model)
		} else {
			// Concurrent fan-out: a second ask arrived before the first finished.
			// Switch to static mode — print the spinner model as a plain line,
			// then this model too, so all candidates are visible at once.
			if r.spin != nil {
				r.stopSpin()
				fmt.Printf("    %-16s  asking...\n", r.spinModel)
			}
			fmt.Printf("    %-16s  asking...\n", e.Model)
		}

	case router.EventAskAccept:
		r.askCount--
		hadSpin := r.spin != nil
		r.stopSpin()
		line := fmt.Sprintf("    %-16s  %s  %.0f%% confident",
			e.Model, r.color("32", "✓ accepted"), e.Confidence*100)
		if e.EstCost > 0 {
			line += fmt.Sprintf(" · ~$%.4f", e.EstCost)
		}
		if e.EstTokens > 0 {
			line += fmt.Sprintf(" · ~%d tokens", e.EstTokens)
		}
		if hadSpin {
			fmt.Printf("\r%-70s\n", line)
		} else {
			fmt.Println(line)
		}

	case router.EventAskReject:
		r.askCount--
		hadSpin := r.spin != nil
		r.stopSpin()
		reason := strings.Join(e.Reasons, ", ")
		line := fmt.Sprintf("    %-16s  %s %s%-20s",
			e.Model, r.color("31", "✗"), reason, "")
		if hadSpin {
			fmt.Printf("\r%-70s\n", line)
		} else {
			fmt.Println(line)
		}

	case router.EventAskError:
		r.askCount--
		hadSpin := r.spin != nil
		r.stopSpin()
		detail := e.Detail
		if detail == "" {
			detail = "parse failure"
		}
		line := fmt.Sprintf("    %-16s  %s %s%-10s",
			e.Model, r.color("33", "!"), compactError(detail), "")
		if hadSpin {
			fmt.Printf("\r%-70s\n", line)
		} else {
			fmt.Println(line)
		}
	}
}

// flushFilters prints the buffered filter events the first time it is called.
// When all candidates passed, prints a single summary line instead of a table —
// the filter phase only has signal when something is pruned.
func (r *Renderer) flushFilters() {
	if r.filterFlushed || len(r.filterBuf) == 0 {
		return
	}
	r.filterFlushed = true

	var fails []filterEntry
	var passes []string
	for _, f := range r.filterBuf {
		if f.pass {
			passes = append(passes, f.model)
		} else {
			fails = append(fails, f)
		}
	}

	fmt.Println()
	if len(fails) == 0 {
		// happy path: no pruning happened — one compact line, not a full table
		fmt.Printf("  %d model(s) qualified: %s\n", len(passes), strings.Join(passes, ", "))
		return
	}

	fmt.Println("  ── Filtering candidates " + strings.Repeat("─", 30))
	fmt.Println()
	for _, f := range fails {
		fmt.Printf("    %-16s  skip  %s\n", f.model, r.color("2", f.reason))
	}
	if len(passes) > 0 {
		fmt.Printf("    %d model(s) qualified\n", len(passes))
	}
}

// compactError trims a wrapped error to a short, single-line hint for the CLI.
func compactError(s string) string {
	s = strings.TrimPrefix(s, "admission gate executor: ")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	if len(s) > 70 {
		s = s[:67] + "..."
	}
	return s
}

// PrintTaskHeader shows the task summary before routing starts.
// kindInferred flags that --kind was not supplied and was auto-detected.
func (r *Renderer) PrintTaskHeader(objective, kind, risk string, maxCost float64, kindInferred bool) {
	if r.quiet {
		return
	}
	fmt.Println()
	obj := objective
	if len(obj) > 60 {
		obj = obj[:57] + "..."
	}
	fmt.Printf("  Routing: %q\n", obj)
	kindStr := kind
	if kindInferred {
		kindStr += r.color("2", " (inferred)")
	}
	line := fmt.Sprintf("  kind: %s  ·  risk: %s", kindStr, risk)
	if maxCost > 0 {
		line += fmt.Sprintf("  ·  max cost: $%.2f", maxCost)
	}
	fmt.Println(line)
}

// PrintResult shows the final selected model after routing completes.
// savedUSD is how much cheaper this model is vs always routing to opus (0 = no saving to show).
func (r *Renderer) PrintResult(model router.ModelCapabilities, decision router.AdmissionDecision, savedUSD float64) {
	if r.quiet {
		return
	}
	fmt.Println()
	fmt.Printf("  → %s (%s tier)\n",
		r.color("32", "Selected: "+model.Name), model.Tier)
	if decision.EstimatedTokens > 0 || decision.EstimatedCostUSD > 0 || savedUSD > 0 {
		parts := []string{}
		if decision.EstimatedTokens > 0 {
			parts = append(parts, fmt.Sprintf("~%d tokens", decision.EstimatedTokens))
		}
		if decision.EstimatedCostUSD > 0 {
			parts = append(parts, fmt.Sprintf("~$%.4f", decision.EstimatedCostUSD))
		}
		if savedUSD > 0 {
			parts = append(parts, r.color("34", fmt.Sprintf("saved ~$%.4f vs opus", savedUSD)))
		}
		fmt.Printf("    %s\n", strings.Join(parts, " · "))
	}
	fmt.Println()
}

func (r *Renderer) stopSpin() {
	if r.spin != nil {
		r.spin.stop()
		r.spin = nil
	}
}

// PrintSkills prints the names of skills that will be injected into the executor prompt.
// No-op when quiet or names is empty.
func (r *Renderer) PrintSkills(names []string) {
	if r.quiet || len(names) == 0 {
		return
	}
	fmt.Printf("  Skills: %s\n", strings.Join(names, ", "))
}

// PrintReview renders the per-criterion verdict after a review pass.
func (r *Renderer) PrintReview(result ReviewResult) {
	if r.quiet {
		return
	}
	fmt.Println()
	fmt.Println("  ── Review " + strings.Repeat("─", 44))
	for _, c := range result.Criteria {
		mark := r.color("32", "✓")
		if !c.Met {
			mark = r.color("31", "✗")
		}
		line := fmt.Sprintf("  %s  %s", mark, c.Criterion)
		if c.Note != "" {
			line += r.color("2", "  — "+c.Note)
		}
		fmt.Println(line)
	}
	fmt.Println()
	if result.Passed {
		fmt.Printf("  %s  All criteria met  (score: %.0f%%)\n", r.color("32", "✓"), result.Score*100)
	} else {
		fmt.Printf("  %s  Review failed  (score: %.0f%%)\n", r.color("31", "✗"), result.Score*100)
	}
	fmt.Println()
}

// spinner drives the animated "asking..." line.
type spinner struct {
	done chan struct{}
	wg   sync.WaitGroup
}

func startSpinner(model string) *spinner {
	frames := []byte{'|', '/', '-', '\\'}
	s := &spinner{done: make(chan struct{})}
	fmt.Printf("    %-16s  %c asking...", model, frames[0])
	tick := time.NewTicker(100 * time.Millisecond)
	s.wg.Go(func() {
		defer tick.Stop()
		i := 1
		for {
			select {
			case <-s.done:
				return
			case <-tick.C:
				fmt.Printf("\r    %-16s  %c asking...", model, frames[i%len(frames)])
				i++
			}
		}
	})
	return s
}

func (s *spinner) stop() {
	close(s.done)
	s.wg.Wait()
}
