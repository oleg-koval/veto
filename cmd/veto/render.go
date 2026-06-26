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

// Renderer displays the routing pipeline in the terminal.
// Set quiet=true or pipe stdout to suppress animation.
type Renderer struct {
	quiet        bool
	isTTY        bool
	filterHeader bool // printed once before first filter event
	askHeader    bool // printed once before first ask event
	mu           sync.Mutex
	spin         *spinner
}

// NewRenderer creates a renderer. Animation is disabled when stdout is not a TTY or quiet=true.
func NewRenderer(quiet bool) *Renderer {
	return &Renderer{
		quiet: quiet,
		isTTY: term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// OnEvent is the event handler — wire it to Manager.OnEvent.
func (r *Renderer) OnEvent(e router.ProgressEvent) {
	if r.quiet {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	switch e.Kind {
	case router.EventFilterPass, router.EventFilterFail:
		if !r.filterHeader {
			fmt.Println()
			fmt.Println("  ── Filtering candidates " + strings.Repeat("─", 30))
			fmt.Println()
			r.filterHeader = true
		}
		if e.Kind == router.EventFilterPass {
			fmt.Printf("    %-16s  pass\n", e.Model)
		} else {
			reason := ""
			if len(e.Reasons) > 0 {
				reason = e.Reasons[0]
			}
			fmt.Printf("    %-16s  skip  %s\n", e.Model, reason)
		}

	case router.EventAskStart:
		if !r.askHeader {
			fmt.Println()
			fmt.Println("  ── Asking models " + strings.Repeat("─", 37))
			fmt.Println()
			r.askHeader = true
		}
		if r.isTTY {
			r.spin = startSpinner(e.Model)
		} else {
			fmt.Printf("    %-16s  asking...\n", e.Model)
		}

	case router.EventAskAccept:
		r.stopSpin()
		line := fmt.Sprintf("    %-16s  ✓ accepted  %.0f%% confident", e.Model, e.Confidence*100)
		if e.EstCost > 0 {
			line += fmt.Sprintf(" · ~$%.4f", e.EstCost)
		}
		if e.EstTokens > 0 {
			line += fmt.Sprintf(" · ~%d tokens", e.EstTokens)
		}
		fmt.Printf("\r%-70s\n", line)

	case router.EventAskReject:
		r.stopSpin()
		reason := strings.Join(e.Reasons, ", ")
		fmt.Printf("\r    %-16s  ✗ %s%-20s\n", e.Model, reason, "")

	case router.EventAskError:
		r.stopSpin()
		fmt.Printf("\r    %-16s  ! error (parse failure)%-10s\n", e.Model, "")
	}
}

// PrintTaskHeader shows the task summary before routing starts.
func (r *Renderer) PrintTaskHeader(objective, kind, risk string, maxCost float64) {
	if r.quiet {
		return
	}
	fmt.Println()
	obj := objective
	if len(obj) > 60 {
		obj = obj[:57] + "..."
	}
	fmt.Printf("  Routing: %q\n", obj)
	line := fmt.Sprintf("  kind: %s  ·  risk: %s", kind, risk)
	if maxCost > 0 {
		line += fmt.Sprintf("  ·  max cost: $%.2f", maxCost)
	}
	fmt.Println(line)
}

// PrintResult shows the final selected model after routing completes.
func (r *Renderer) PrintResult(model router.ModelCapabilities, decision router.AdmissionDecision) {
	if r.quiet {
		return
	}
	fmt.Println()
	fmt.Printf("  → Selected: %s (%s tier)\n", model.Name, model.Tier)
	if decision.EstimatedTokens > 0 || decision.EstimatedCostUSD > 0 {
		parts := []string{}
		if decision.EstimatedTokens > 0 {
			parts = append(parts, fmt.Sprintf("~%d tokens", decision.EstimatedTokens))
		}
		if decision.EstimatedCostUSD > 0 {
			parts = append(parts, fmt.Sprintf("~$%.4f", decision.EstimatedCostUSD))
		}
		fmt.Printf("    Estimated: %s\n", strings.Join(parts, " · "))
	}
	fmt.Println()
}

func (r *Renderer) stopSpin() {
	if r.spin != nil {
		r.spin.stop()
		r.spin = nil
	}
}

// spinner drives the animated "asking..." line.
type spinner struct {
	done chan struct{}
	wg   sync.WaitGroup
}

func startSpinner(model string) *spinner {
	frames := []byte{'|', '/', '-', '\\'}
	s := &spinner{done: make(chan struct{})}
	// print initial frame immediately
	fmt.Printf("    %-16s  %c asking...", model, frames[0])
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		i := 1
		for {
			select {
			case <-s.done:
				return
			case <-time.After(100 * time.Millisecond):
				fmt.Printf("\r    %-16s  %c asking...", model, frames[i%len(frames)])
				i++
			}
		}
	}()
	return s
}

func (s *spinner) stop() {
	close(s.done)
	s.wg.Wait()
}
