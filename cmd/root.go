// Package cmd is the maxos command line: Cobra commands, and the Uber Fx
// assembly of the modules each command runs. It is the only place that knows the
// concrete implementations and injects them into the ports.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the command line and exits with status 1 when the command fails.
// Cobra reports the error on stderr; stdout belongs to the MCP protocol in
// mcp-stdio mode.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "maxos",
		Short: "Personal system: an MCP server in front of PostgreSQL",
		// A failed command is not a usage mistake; the error alone is enough.
		SilenceUsage: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newMigrateCmd(), newServeCmd(), newMCPStdioCmd())
	return root
}
