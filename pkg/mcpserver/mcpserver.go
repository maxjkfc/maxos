// Package mcpserver serves MCP tools over Streamable HTTP and stdio.
//
// It knows nothing about any bounded context: each context contributes a
// Registrar that adds its tools to a server, and those tools act on behalf of
// the caller the transport identified.
package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Caller identifies who is making MCP calls, so tools can attribute what they
// write.
type Caller struct {
	// Source is the channel the calls arrive through, e.g. "mcp-http".
	Source string
	// Actor is the identity behind the calls.
	Actor string
}

// CallerFunc identifies the caller of an HTTP request. It reports false when it
// cannot.
type CallerFunc func(r *http.Request) (Caller, bool)

// Registrar adds one context's tools to server. The tools act on behalf of
// caller.
type Registrar func(server *mcp.Server, caller Caller)

// Registry builds MCP servers that carry the tools of every registrar.
type Registry struct {
	name       string
	registrars []Registrar
}

// NewRegistry returns a Registry whose servers announce themselves as name.
func NewRegistry(name string, registrars []Registrar) *Registry {
	return &Registry{name: name, registrars: registrars}
}

// HTTPHandler serves MCP over Streamable HTTP in stateless mode: every request
// gets its own server, bound to the caller that callerOf identifies. A request
// whose caller cannot be identified is refused (the SDK answers 400) rather
// than served under an anonymous actor.
func (r *Registry) HTTPHandler(callerOf CallerFunc) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		caller, ok := callerOf(req)
		if !ok {
			return nil
		}
		return r.newServer(caller)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
}

// RunStdio serves a single MCP session over stdin and stdout until the client
// disconnects or ctx ends.
func (r *Registry) RunStdio(ctx context.Context, caller Caller) error {
	return r.newServer(caller).Run(ctx, &mcp.StdioTransport{})
}

func (r *Registry) newServer(caller Caller) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: r.name}, nil)
	for _, register := range r.registrars {
		register(server, caller)
	}
	return server
}
