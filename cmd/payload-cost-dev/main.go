// Command payload-cost-dev attributes the preserved development-only
// task_context/2 responses. It cannot accept the sealed dataset or recapture a
// response; its only input is the already committed two-build dev artifact.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

func main() {
	input := flag.String("input", "", "development-only bundles-after.json")
	output := flag.String("out", "", "output report path (default stdout)")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "payload-cost-dev: -input is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		fatal(err)
	}
	counter, err := retrieval.LoadPinnedRealPayloadCounter()
	if err != nil {
		fatal(err)
	}
	report, err := retrieval.BuildDevPayloadCostReport(raw, counter)
	if err != nil {
		fatal(err)
	}
	encoded, err := retrieval.MarshalPayloadCostReport(report)
	if err != nil {
		fatal(err)
	}
	if *output == "" {
		_, err = os.Stdout.Write(encoded)
	} else {
		err = os.WriteFile(*output, encoded, 0o644)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "payload-cost-dev: %v\n", err)
	os.Exit(1)
}
