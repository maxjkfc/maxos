package cmd_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/webtest"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// httpHandler is what the server serves, made by the module that serve uses, with the
// registry of the pages empty and the one of the MCP tools holding only the tools that
// the test gives: what is under test is what stands in front of them. The Access
// verifier is the stand-in that trusts a key of the test.
func httpHandler(t *testing.T, access *webtest.Access, tools ...mcpserver.Registrar) http.Handler {
	t.Helper()
	var handler http.Handler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() *cfaccess.Verifier { return access.Verifier() },
			func() *mcpserver.Registry { return mcpserver.NewRegistry("maxos", tools) },
			func() *webserver.Registry { return webserver.NewRegistry(slog.New(slog.DiscardHandler), nil) },
		),
		cmd.HTTPModule(),
		fx.Populate(&handler),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the handler: %v", err)
	}
	return handler
}

// serveOn runs handler in the server that serve runs (cmd.NewHTTPServer) on a port of
// this machine, until the test ends, and returns its address.
func serveOn(t *testing.T, handler http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := cmd.NewHTTPServer(ln.Addr().String(), handler)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// securityHeaders are the headers that every response carries with one value: the
// Cache-Control, which the MCP endpoint says otherwise, and the policy are checked
// apart (wantHeaders).
var securityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"X-Frame-Options":        "DENY",
}

// mcpCacheControl is the Cache-Control of what the MCP endpoint answers: the SDK sets
// its own on the answer to a call, and webserver.NoStore puts it right.
const mcpCacheControl = "no-store, no-transform"

// wantHeaders checks the five headers of a response, with the Cache-Control that this
// response is to have.
func wantHeaders(t *testing.T, what string, h http.Header, cacheControl string) {
	t.Helper()
	for name, want := range securityHeaders {
		if got := h.Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
	if got := h.Get("Cache-Control"); got != cacheControl {
		t.Errorf("%s: Cache-Control = %q, want %q", what, got, cacheControl)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("%s: Content-Security-Policy = %q, want default-src 'self'", what, csp)
	}
}

// wantSecurityHeaders checks the five headers of a response that is not one of the MCP
// endpoint's own: a refusal, a page, what the mux answers.
func wantSecurityHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	wantHeaders(t, what, h, "no-store")
}

// TestNothingIsAnsweredBeforeTheVerifierHasLooked goes over the paths that the mux of
// net/http answers on its own, a redirect for a path that is not clean and a 404 for a
// path that nothing serves, and the paths that are served: with no token every one of
// them is refused and told nothing else, and every response has the security headers.
func TestNothingIsAnsweredBeforeTheVerifierHasLooked(t *testing.T) {
	access := webtest.NewAccess(t)
	handler := httpHandler(t, access)
	paths := []string{
		// The mux redirects these to the clean form of the path.
		"//app", "/app/./x", "/app/../mcp", "//mcp", "/app//transactions", "/mcp/../app/",
		// The mux has no route for these, and answers 404.
		"/", "/APP/", "/APP", "/mcp/", "/app%2fx", "/nowhere", "/favicon.ico", "/app.css",
		// And these are served.
		"/mcp", "/app", "/app/", "/app/transactions", "/app/static/app.css",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
			name := method + " " + path
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))

				if rec.Code != http.StatusUnauthorized {
					t.Errorf("%s without a token = %d, want 401: nothing but the refusal is said without one\n%s", name, rec.Code, rec.Body)
				}
				if loc := rec.Header().Get("Location"); loc != "" {
					t.Errorf("%s without a token redirects to %q: which paths exist is not for an unknown caller to learn", name, loc)
				}
				wantSecurityHeaders(t, name+" without a token", rec.Header())
			})
		}
	}
}

// TestWhatTheMuxAnswersIsAnsweredToWhoIsSignedInWithTheHeaders: a request with a token
// that is accepted gets what the mux says, and that has the headers as well.
func TestWhatTheMuxAnswersIsAnsweredToWhoIsSignedInWithTheHeaders(t *testing.T) {
	access := webtest.NewAccess(t)
	handler := httpHandler(t, access)
	tests := []struct {
		path       string
		wantStatus int
	}{
		{"//app", http.StatusTemporaryRedirect}, // the clean form of the path
		{"/app/./x", http.StatusTemporaryRedirect},
		{"/", http.StatusNotFound},
		{"/APP/", http.StatusNotFound},
		{"/mcp/", http.StatusNotFound},
		{"/app", http.StatusMovedPermanently}, // the pages send it on to /app/
		{"/app/nowhere", http.StatusNotFound},
		{"/app/static/app.css", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, access.Request(t, http.MethodGet, tt.path, "max@example.com"))

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s signed in = %d, want %d\n%s", tt.path, rec.Code, tt.wantStatus, rec.Body)
			}
			wantSecurityHeaders(t, "GET "+tt.path+" signed in", rec.Header())
		})
	}
}

// optionsStar sends "OPTIONS *" over a socket of its own, as a client that is not
// net/http's may, with the token if there is one, and returns the answer and its body.
func optionsStar(t *testing.T, addr, token string) (*http.Response, string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set the deadline: %v", err)
	}
	request := "OPTIONS * HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n"
	if token != "" {
		request += webtest.Header + ": " + token + "\r\n"
	}
	if _, err := io.WriteString(conn, request+"\r\n"); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodOptions})
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read the body of the answer: %v", err)
	}
	return res, string(body)
}

// TestOptionsStarIsAnsweredByTheHandlerLikeAnyOtherRequest: net/http answers
// "OPTIONS *" with a 200 before it asks the handler, unless the server is told not
// to, and the server that serve runs is. So it goes to the handler: without a token
// the answer is the refusal of every other request, and with one it is the mux's own
// (400: there is no path to route), and both carry the headers.
func TestOptionsStarIsAnsweredByTheHandlerLikeAnyOtherRequest(t *testing.T) {
	access := webtest.NewAccess(t)
	addr := serveOn(t, httpHandler(t, access))

	t.Run("without a token", func(t *testing.T) {
		res, body := optionsStar(t, addr, "")

		if res.StatusCode != http.StatusUnauthorized || body != "unauthorized\n" {
			t.Errorf("OPTIONS * without a token = %d %q, want the 401 of every request without one", res.StatusCode, body)
		}
		wantSecurityHeaders(t, "OPTIONS * without a token", res.Header)
	})

	t.Run("with a token", func(t *testing.T) {
		res, body := optionsStar(t, addr, access.Token(t, "max@example.com"))

		if res.StatusCode != http.StatusBadRequest || body != "" {
			t.Errorf("OPTIONS * with a token = %d %q, want the 400 without a body that the mux answers", res.StatusCode, body)
		}
		wantSecurityHeaders(t, "OPTIONS * with a token", res.Header)
	})
}

// exchange is a request that an MCP client made and what the server answered.
type exchange struct {
	method string // of HTTP
	call   string // the method of the JSON-RPC message that was sent; empty for a GET
	status int
	header http.Header
}

// signedIn is the transport of an MCP client that is signed in: every request carries
// a token, as Access sends one with each request it lets through, and the server's
// answers are kept.
type signedIn struct {
	token string
	base  http.Transport

	mu   sync.Mutex
	seen []exchange
}

func (s *signedIn) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(webtest.Header, s.token)
	var call string
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		var message struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &message) // what is not a message has no method
		call = message.Method
	}
	res, err := s.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, exchange{method: req.Method, call: call, status: res.StatusCode, header: res.Header.Clone()})
	return res, nil
}

// answers returns what the server has answered so far.
func (s *signedIn) answers() []exchange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]exchange(nil), s.seen...)
}

// answersTo returns what the server answered to the JSON-RPC messages with this method.
func (s *signedIn) answersTo(call string) []exchange {
	var found []exchange
	for _, e := range s.answers() {
		if e.method == http.MethodPost && e.call == call {
			found = append(found, e)
		}
	}
	return found
}

// connectMCP has an MCP client of the SDK sign in to the server at addr as max, and
// do the handshake of the protocol version: the newest that the SDK speaks when it
// is empty. It returns the session, ended when the test ends, and what the server
// answers to it.
func connectMCP(ctx context.Context, t *testing.T, access *webtest.Access, addr, version string, opts *mcp.ClientOptions) (*mcp.ClientSession, *signedIn) {
	t.Helper()
	transport := &signedIn{token: access.Token(t, "max@example.com")}
	t.Cleanup(transport.base.CloseIdleConnections)
	client := mcp.NewClient(&mcp.Implementation{Name: "maxos-test", Version: "0"}, opts)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   "http://" + addr + "/mcp",
		HTTPClient: &http.Client{Transport: transport},
	}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connect to /mcp (protocol %q): %v", version, err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, transport
}

// wantEveryAnswerHeld checks the headers of every answer that the server gave to an
// MCP client, and that those to the calls were event streams, which is what the SDK
// answers a call with and what webserver.NoStore has to let through whole.
func wantEveryAnswerHeld(t *testing.T, transport *signedIn) {
	t.Helper()
	for _, e := range transport.answers() {
		what := fmt.Sprintf("%s /mcp %s", e.method, e.call)
		wantHeaders(t, what, e.header, mcpCacheControl)
		if e.status == http.StatusOK {
			if ct := e.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
				t.Errorf("%s: Content-Type = %q, want text/event-stream", what, ct)
			}
		}
	}
}

// TestMCPEventsReachTheClientWhileTheCallRuns: the tool of a call sends an event and
// then waits until the client has it. What the SDK writes goes through the writer of
// webserver.NoStore, and each event is flushed through it: if it did not get through,
// the event would sit in the buffer of the server until the call is over, and the
// tool would wait in vain.
func TestMCPEventsReachTheClientWhileTheCallRuns(t *testing.T) {
	access := webtest.NewAccess(t)
	seen := make(chan struct{})
	var once sync.Once
	slow := func(server *mcp.Server, _ mcpserver.Caller) {
		mcp.AddTool(server, &mcp.Tool{Name: "slow"}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			err := req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: req.Params.GetProgressToken(), Message: "first"})
			if err != nil {
				return nil, nil, err
			}
			select {
			case <-seen:
			case <-time.After(5 * time.Second):
				return nil, nil, errors.New("the first event had not reached the client five seconds after it was sent")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "second"}}}, nil, nil
		})
	}
	addr := serveOn(t, httpHandler(t, access, slow))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, transport := connectMCP(ctx, t, access, addr, "", &mcp.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) {
			once.Do(func() { close(seen) })
		},
	})

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "slow", Meta: mcp.Meta{"progressToken": "slow-1"}})

	if err != nil {
		t.Fatalf("call slow: %v", err)
	}
	if got := toolText(res); res.IsError || got != "second" {
		t.Errorf("slow = %q (error %v), want the second event, %q", got, res.IsError, "second")
	}
	if got := len(transport.answersTo("tools/call")); got != 1 {
		t.Errorf("the server answered %d calls, want 1", got)
	}
	wantEveryAnswerHeld(t, transport)
}

// toolText is the text of the first content of a result of a tool.
func toolText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	text, _ := res.Content[0].(*mcp.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}
