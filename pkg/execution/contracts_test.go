package execution

import "testing"

func TestExecutionOptionsDefaultIsBounded(t *testing.T) {
	tests := []struct {
		name    string
		options ExecutionOptions
		want    int
	}{
		{name: "zero", want: DefaultExecutionMaxTokens},
		{name: "negative", options: ExecutionOptions{MaxOutputTokens: -1}, want: DefaultExecutionMaxTokens},
		{name: "explicit", options: ExecutionOptions{MaxOutputTokens: 4096}, want: 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.options.EffectiveMaxOutputTokens(); got != tt.want {
				t.Fatalf("EffectiveMaxOutputTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

// Compile-time checks make the contract package's shape explicit and prevent
// accidental migration back to concrete provider types.
var (
	_ TaskExecutor      = nil
	_ EventTaskExecutor = nil
	_ RuntimeAdapter    = nil
	_ ToolProvider      = nil
)
