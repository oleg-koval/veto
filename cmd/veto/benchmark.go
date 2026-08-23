package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/oleg-koval/veto/internal/eval"
)

const defaultBenchmarkCorpus = "internal/eval/testdata/routing_corpus.json"

// cmdBenchmark replays a fixed corpus without loading credentials or providers.
// Output is always JSON so it can be consumed by CI and other tooling.
func cmdBenchmark(args []string) {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	corpusPath := fs.String("corpus", defaultBenchmarkCorpus, "offline routing corpus JSON file")
	_ = fs.Parse(args)

	corpus, err := eval.LoadFile(*corpusPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: %v\n", err)
		os.Exit(1)
	}
	if err := eval.WriteJSON(os.Stdout, eval.Evaluate(corpus)); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: write report: %v\n", err)
		os.Exit(1)
	}
}
