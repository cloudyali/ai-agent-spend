// Command pricing-sync curates + validates the upstream LiteLLM price table and emits
// the files published to the aispend pricing mirror (aispendllm.cloudyali.io). It is CI
// tooling — NOT shipped in the aispend binary — and imports no net/*: the workflow
// downloads upstream, this validates + builds. The validation gate (see
// internal/pricesync) is the circuit breaker that keeps a bad upstream from ever
// reaching users, so a failure here must hold the publish, not push a broken table.
//
// Exit codes: 0 = published, 1 = held (validation failed or error), 2 = usage error.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/pricesync"
)

func main() {
	in := flag.String("in", "", "path to the freshly downloaded upstream LiteLLM JSON (required)")
	prev := flag.String("prev", "", "path to the previously published litellm.json, for the safety diff (optional)")
	out := flag.String("out", "dist", "output directory for published artifacts")
	flag.Parse()

	if *in == "" {
		fmt.Fprintln(os.Stderr, "pricing-sync: -in is required")
		flag.Usage()
		os.Exit(2)
	}

	rep, err := pricesync.Run(*in, *prev, *out, time.Now(), pricesync.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pricing-sync:", err)
		os.Exit(1)
	}
	fmt.Printf("models: %d (prev %d) · added %d · removed %d · repriced %d · swings %d\n",
		rep.CurrentModels, rep.PreviousModels, rep.Added, rep.Removed, rep.Repriced, len(rep.Warnings))

	// Surface out-of-band swings so a wrongly-accepted (or systemically corrupt)
	// price is visible in the Actions log, not silent. The outcome line below says
	// whether they were waved through (OK) or held (a systemic violation).
	const maxShow = 10
	for i, w := range rep.Warnings {
		if i == maxShow {
			fmt.Fprintf(os.Stderr, "  ~ ...and %d more\n", len(rep.Warnings)-maxShow)
			break
		}
		fmt.Fprintln(os.Stderr, "  ~ swing: "+w)
	}

	if !rep.OK() {
		fmt.Fprintln(os.Stderr, "pricing-sync: VALIDATION FAILED — refusing to publish:")
		for _, v := range rep.Violations {
			fmt.Fprintln(os.Stderr, "  - "+v)
		}
		os.Exit(1)
	}
	fmt.Printf("OK — published %d models to %s/\n", rep.CurrentModels, *out)
}
