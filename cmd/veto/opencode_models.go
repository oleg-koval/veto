package main

import (
	"fmt"

	"github.com/oleg-koval/veto/pkg/opencode"
	"github.com/oleg-koval/veto/pkg/router"
)

func addOpenCodeModels(
	reg *providerRegistry,
	config opencode.Config,
	discovery opencode.Discovery,
	deps opencode.Dependencies,
	disabled map[string]bool,
) {
	known := make(map[string]router.ModelCapabilities, len(reg.caps))
	for _, capability := range router.NewRegistry().All() {
		known[capability.Provider+"\x00"+capability.APIModel] = capability
	}
	for _, capability := range reg.caps {
		known[capability.Provider+"\x00"+capability.APIModel] = capability
	}
	for _, model := range discovery.Models {
		name := openCodeModelName(model)
		if disabled[name] {
			continue
		}
		capability, ok := known[model.Provider+"\x00"+model.ID]
		if !ok {
			capability = router.ModelCapabilities{
				CostPer1kInputUnknown: true, CostPer1kOutputUnknown: true,
			}
		}
		capability.Name = name
		capability.Source = "opencode"
		capability.Provider = model.Provider
		capability.APIModel = model.ID
		capability.Runtime = ""
		capability.SupportsTools = nil
		reg.caps[name] = capability
		reg.executors[name] = opencode.NewRuntime(config, discovery, model, deps)
	}
}

func openCodeModelName(model opencode.Model) string {
	return fmt.Sprintf("opencode:%s/%s", model.Provider, model.ID)
}
