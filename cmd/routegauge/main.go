// Command routegauge turns rotated access logs into an API analytics
// report: clustered endpoints, latency percentiles, and error rates.
package main

import (
	"os"

	"github.com/JaydenCJ/routegauge/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
