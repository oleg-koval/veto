package router

import "testing"

func TestRequiresExecutableRuntime(t *testing.T) {
	for _, test := range []struct {
		objective string
		want      bool
	}{
		{objective: "fix all review comments in this PR and push", want: true},
		{objective: "modify the repository and commit the changes", want: true},
		{objective: "write a Go function that parses a duration", want: false},
	} {
		if got := RequiresExecutableRuntime(test.objective); got != test.want {
			t.Errorf("RequiresExecutableRuntime(%q) = %v, want %v", test.objective, got, test.want)
		}
	}
}
