package router

import (
	"fmt"
	"testing"
)

// BenchmarkSignal_ByHistorySize shows how Signal() degrades as the event log grows.
// Signal() does a full O(H) scan of s.events on every call; this makes the slope visible.
// Expected output: ns/op roughly linear in H — motivation for the O(1) aggregated-stats fix.
func BenchmarkSignal_ByHistorySize(b *testing.B) {
	models := []string{"haiku", "sonnet", "opus"}
	for _, n := range []int{10, 100, 1000, 5000} {
		b.Run(fmt.Sprintf("events=%d", n), func(b *testing.B) {
			store := NewMemoryStore()
			for i := range n {
				m := models[i%len(models)]
				store.LogDecision(fmt.Sprintf("task-%d", i), m, AdmissionDecision{
					Accept:     i%3 != 0,
					Confidence: 0.9,
				})
			}
			b.ReportAllocs()
			for b.Loop() {
				store.Signal("sonnet", KindCodeChange)
			}
		})
	}
}

// BenchmarkRankCandidates measures the full filter → score → sort pipeline.
// Called once per Route() invocation before the admission loop starts.
func BenchmarkRankCandidates(b *testing.B) {
	reg := NewRegistry()
	models := reg.All()
	task := TaskSpec{
		ID:        "bench-task",
		Kind:      KindCodeChange,
		MaxTokens: 10000,
	}
	b.ReportAllocs()
	for b.Loop() {
		RankCandidates(task, models, reg)
	}
}

// BenchmarkRankCandidates_WithHistory measures ranking when Signal() must scan real history.
// Combines the O(C×H) cost: RankCandidates calls Signal() once per candidate.
func BenchmarkRankCandidates_WithHistory(b *testing.B) {
	models := []string{"haiku", "sonnet", "opus"}
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("events=%d", n), func(b *testing.B) {
			store := NewMemoryStore()
			for i := range n {
				m := models[i%len(models)]
				store.LogDecision(fmt.Sprintf("task-%d", i), m, AdmissionDecision{
					Accept: i%3 != 0, Confidence: 0.9,
				})
			}
			reg := NewRegistry()
			all := reg.All()
			task := TaskSpec{ID: "t", Kind: KindCodeChange, MaxTokens: 10000}
			b.ReportAllocs()
			for b.Loop() {
				RankCandidates(task, all, store)
			}
		})
	}
}

// BenchmarkScore measures the weighted scoring formula for a single model.
// Called C times per RankCandidates (once per candidate).
func BenchmarkScore(b *testing.B) {
	reg := NewRegistry()
	models := reg.All()
	task := TaskSpec{Kind: KindCodeChange, MaxCostUSD: 0.01, MaxTokens: 5000}
	sig := RoutingSignal{
		HistoricalSuccessRate: 0.8,
		HistoricalRejectRate:  0.1,
		AvgEvalScore:          0.75,
	}
	b.ReportAllocs()
	for b.Loop() {
		Score(task, models[1], sig)
	}
}

// BenchmarkParseAdmissionJSON covers the two common output shapes:
//   - clean: model returns bare JSON (ideal)
//   - framed: model prepends/appends prose (common with larger models)
func BenchmarkParseAdmissionJSON(b *testing.B) {
	clean := `{"accept":true,"confidence":0.9,"reason_codes":[],"estimated_tokens":500,"estimated_cost_usd":0.001}`
	framed := `Here is my evaluation of the task. ` + clean + ` I hope that helps.`

	b.Run("clean", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			parseAdmissionJSON(clean)
		}
	})
	b.Run("framed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			parseAdmissionJSON(framed)
		}
	})
}
