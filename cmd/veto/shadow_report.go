package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	shadowdata "github.com/oleg-koval/veto/pkg/shadow"
)

func runShadowReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("shadow-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", defaultShadowEvidencePath(), "shadow evidence JSONL file")
	fallbackConfidence := fs.Float64("fallback-confidence", shadowdata.DefaultFallbackConfidence, "simulated hybrid fallback threshold")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	report, err := evaluateShadowReport(*input, *fallbackConfidence)
	if err != nil {
		fmt.Fprintf(stderr, "shadow-report: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		fmt.Fprintf(stderr, "shadow-report: encode report: %v\n", err)
		return 1
	}
	return 0
}

func evaluateShadowReport(input string, fallbackConfidence float64) (shadowdata.Report, error) {
	file, err := os.Open(input)
	if err != nil {
		return shadowdata.Report{}, fmt.Errorf("open evidence: %w", err)
	}
	defer file.Close()
	events, err := shadowdata.Load(file)
	if err != nil {
		return shadowdata.Report{}, err
	}
	dataset, err := shadowdata.Materialize(events)
	if err != nil {
		return shadowdata.Report{}, err
	}
	return shadowdata.Evaluate(dataset, fallbackConfidence), nil
}
