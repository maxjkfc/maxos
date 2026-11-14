// Package mcp exposes the inbox use cases as MCP tools. It only converts between
// the tool formats and the use case inputs and outputs, and maps errors to
// messages a model can act on.
package mcp

import (
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
)

// NewRegistrar returns the registrar that adds the inbox tools, capture and
// inbox_recent, to an MCP server.
func NewRegistrar(capture usecase.Capture, recent usecase.ListRecent) mcpserver.Registrar {
	return func(s *sdk.Server, caller mcpserver.Caller) {
		addCaptureTool(s, caller, capture)
		addInboxRecentTool(s, recent)
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
