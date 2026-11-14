// Package log builds the process logger.
//
// Every record goes to stderr and nothing else is ever written to stdout: in
// mcp-stdio mode stdout carries the MCP protocol.
package log

import (
	"log/slog"
	"os"
)

// New returns a logger that writes text records to stderr.
func New() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}
