package router

import "testing"

func TestInferKindMatchesCLIObjectivePolicy(t *testing.T) {
	tests := []struct {
		objective string
		want      TaskKind
	}{
		{"fix the startup crash", KindDebug},
		{"refactor the auth package", KindRefactor},
		{"summarize this incident", KindSummarize},
		{"extract table rows", KindExtract},
		{"review the payment code", KindReview},
		{"design the migration", KindPlan},
		{"add a retry", KindCodeChange},
	}
	for _, test := range tests {
		t.Run(test.objective, func(t *testing.T) {
			if got := InferKind(test.objective); got != test.want {
				t.Fatalf("InferKind(%q) = %q, want %q", test.objective, got, test.want)
			}
		})
	}
}
