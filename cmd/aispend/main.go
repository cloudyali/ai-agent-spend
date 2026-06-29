// Command aispend is the aispend CLI: a trusted, explainable, local-by-default
// ledger for AI-coding spend. The command tree lives in internal/cli; this
// entrypoint intentionally pulls in no network imports — see
// design-documents/DESIGN.md (Trust requirements).
package main

import (
	"os"

	"github.com/cloudyali/ai-agent-spend/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
