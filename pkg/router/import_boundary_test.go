package router

import (
	"os/exec"
	"strings"
	"testing"
)

func TestImportBoundaryDoesNotDependOnOuterPackages(t *testing.T) {
	output, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect router imports: %v\n%s", err, output)
	}

	const (
		modulePrefix     = "github.com/oleg-koval/veto/"
		executionPackage = modulePrefix + "pkg/execution"
	)
	for _, importPath := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.HasPrefix(importPath, modulePrefix) && importPath != executionPackage {
			t.Fatalf("pkg/router imports outward dependency %q", importPath)
		}
	}
}
