package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/oleg-koval/veto/pkg/router"
)

const modelsAPIVersion = 1

type modelListItem struct {
	Name                 string   `json:"name"`
	Source               string   `json:"source"`
	Provider             string   `json:"provider"`
	APIModel             string   `json:"api_model"`
	Runtime              string   `json:"runtime"`
	Tier                 string   `json:"tier"`
	MaxContextTokens     int      `json:"max_context_tokens"`
	SupportsTools        []string `json:"supports_tools"`
	ToolsKnown           bool     `json:"tools_known"`
	CostPer1kInputUSD    float64  `json:"cost_per_1k_input_usd"`
	CostPer1kInputKnown  bool     `json:"cost_per_1k_input_known"`
	CostPer1kOutputUSD   float64  `json:"cost_per_1k_output_usd"`
	CostPer1kOutputKnown bool     `json:"cost_per_1k_output_known"`
}

type modelListResult struct {
	APIVersion int             `json:"api_version"`
	Models     []modelListItem `json:"models"`
}

func cmdModels(args []string) {
	code := runModelsCommand(args, os.Stdout, os.Stderr, buildProviderRegistryWithCatalog)
	if code != 0 {
		os.Exit(code)
	}
}

func runModelsCommand(args []string, stdout, stderr io.Writer, build func(bool) (*providerRegistry, error)) int {
	flags := flag.NewFlagSet("models", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit a stable machine-readable model list")
	offline := flags.Bool("offline", false, "use built-in and cached model metadata without catalog network access")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "models does not accept positional arguments")
		return 2
	}
	reg, err := build(*offline)
	if err != nil {
		fmt.Fprintf(stderr, "list models: %v\n", err)
		return 1
	}
	result := makeModelList(reg.modelCaps())
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "encode models: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%-30s %-12s %-14s %-12s %s\n", "model", "provider", "runtime", "tier", "context")
	for _, model := range result.Models {
		context := "unknown"
		if model.MaxContextTokens > 0 {
			context = fmt.Sprintf("%d", model.MaxContextTokens)
		}
		fmt.Fprintf(stdout, "%-30s %-12s %-14s %-12s %s\n", model.Name, model.Provider, model.Runtime, model.Tier, context)
	}
	return 0
}

func makeModelList(caps []router.ModelCapabilities) modelListResult {
	items := make([]modelListItem, 0, len(caps))
	for _, model := range caps {
		items = append(items, modelListItem{
			Name:                 model.Name,
			Source:               model.Source,
			Provider:             model.Provider,
			APIModel:             model.Identity().Model,
			Runtime:              model.Runtime,
			Tier:                 model.Tier,
			MaxContextTokens:     model.MaxContextTokens,
			SupportsTools:        model.SupportsTools,
			ToolsKnown:           model.SupportsTools != nil,
			CostPer1kInputUSD:    model.CostPer1kInputUSD,
			CostPer1kInputKnown:  !model.CostPer1kInputUnknown,
			CostPer1kOutputUSD:   model.CostPer1kOutputUSD,
			CostPer1kOutputKnown: !model.CostPer1kOutputUnknown,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		if items[i].Runtime != items[j].Runtime {
			return items[i].Runtime < items[j].Runtime
		}
		return items[i].APIModel < items[j].APIModel
	})
	return modelListResult{APIVersion: modelsAPIVersion, Models: items}
}
