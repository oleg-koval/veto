package router_test

import (
	"context"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router"
)

type legacyExecutor struct{}

func (legacyExecutor) Run(context.Context, string) executor.Result {
	return executor.Result{}
}

var _ router.Executor = legacyExecutor{}

func TestExecutorResultAliasPreservesPublicGateCompatibility(t *testing.T) {
	if gate := router.NewAdmissionGate(legacyExecutor{}); gate == nil {
		t.Fatal("NewAdmissionGate returned nil")
	}
}
