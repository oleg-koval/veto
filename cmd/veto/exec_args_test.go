package main

import (
	"flag"
	"strings"
	"testing"
)

func TestParseExecPlanArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		path    string
		dryRun  bool
		quiet   bool
		timeout string
		wantErr string
	}{
		{name: "flags before plan", args: []string{"--dry-run", "--quiet", "plan.md"}, path: "plan.md", dryRun: true, quiet: true},
		{name: "flags after plan", args: []string{"plan.md", "--dry-run", "--quiet", "--timeout", "5s"}, path: "plan.md", dryRun: true, quiet: true, timeout: "5s"},
		{name: "flags on both sides", args: []string{"--quiet", "plan.md", "--dry-run"}, path: "plan.md", dryRun: true, quiet: true},
		{name: "end of flags before plan", args: []string{"--", "plan.md"}, path: "plan.md"},
		{name: "end of flags rejects trailing option", args: []string{"--", "plan.md", "--dry-run"}, wantErr: "unexpected argument"},
		{name: "end of flags after plan", args: []string{"plan.md", "--", "--dry-run"}, wantErr: "unexpected argument"},
		{name: "missing plan", args: []string{"--dry-run"}, wantErr: "provide a plan file"},
		{name: "extra plan", args: []string{"plan.md", "other.md"}, wantErr: "unexpected argument"},
		{name: "unknown trailing flag", args: []string{"plan.md", "--unknown"}, wantErr: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("exec", flag.ContinueOnError)
			fs.SetOutput(new(strings.Builder))
			dryRun := fs.Bool("dry-run", false, "")
			quiet := fs.Bool("quiet", false, "")
			timeout := fs.String("timeout", "", "")
			path, err := parseExecPlanArgs(fs, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if path != tt.path || *dryRun != tt.dryRun || *quiet != tt.quiet || *timeout != tt.timeout {
				t.Fatalf("got path=%q dry-run=%t quiet=%t timeout=%q", path, *dryRun, *quiet, *timeout)
			}
		})
	}
}
