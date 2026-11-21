package webserver_test

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/pkg/webserver"
)

// get answers a GET with h behind a real server: a recorder commits a response in a
// way of its own, and what is under test is when net/http commits one.
func get(t *testing.T, h http.Handler) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	return res, string(body)
}

// TestNoStoreHasTheLastWordOnCacheControl: a handler that loosens the header, as the
// MCP SDK does, and then commits the response in each way there is, still answers
// no-store, and the other headers of Secure are as they were.
func TestNoStoreHasTheLastWordOnCacheControl(t *testing.T) {
	loosen := func(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-cache, no-transform") }
	tests := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"WriteHeader", func(w http.ResponseWriter, _ *http.Request) { loosen(w); w.WriteHeader(http.StatusAccepted) }},
		{"the first Write", func(w http.ResponseWriter, _ *http.Request) { loosen(w); _, _ = io.WriteString(w, "body") }},
		{"a Flush before any write", func(w http.ResponseWriter, _ *http.Request) {
			loosen(w)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush: %v", err)
			}
		}},
		{"http.Error", func(w http.ResponseWriter, _ *http.Request) {
			loosen(w)
			http.Error(w, "refused", http.StatusBadRequest)
		}},
		{"nothing written", func(w http.ResponseWriter, _ *http.Request) { loosen(w) }},
		{"a header that was not set", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := get(t, webserver.Secure(webserver.NoStore(tt.h)))

			if got, want := res.Header.Get("Cache-Control"), "no-store, no-transform"; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
			for name, want := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":        "DENY",
			} {
				if got := res.Header.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
				t.Errorf("Content-Security-Policy = %q, want default-src 'self'", csp)
			}
		})
	}
}

// TestNoStoreLeavesTheRestOfTheResponseAlone: the status, the other headers and the
// body of what next wrote are the ones the client gets.
func TestNoStoreLeavesTheRestOfTheResponseAlone(t *testing.T) {
	res, body := get(t, webserver.NoStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Other", "kept")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "one,")
		_, _ = io.WriteString(w, "two")
	})))

	if res.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := res.Header.Get("X-Other"); got != "kept" {
		t.Errorf("X-Other = %q, want kept", got)
	}
	if body != "one,two" {
		t.Errorf("body = %q, want %q", body, "one,two")
	}
}

// TestNoStoreLetsAStreamThrough: what a handler flushes reaches the client while the
// handler is still running, which is what the MCP SDK relies on for every event it
// sends, and the other methods of an http.ResponseController reach the connection.
func TestNoStoreLetsAStreamThrough(t *testing.T) {
	seen := make(chan struct{})
	srv := httptest.NewServer(webserver.NoStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		_, _ = io.WriteString(w, "first\n")
		if err := rc.Flush(); err != nil {
			t.Errorf("Flush: %v", err)
			return
		}
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			t.Errorf("SetWriteDeadline: %v: the controller does not reach the connection", err)
		}
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
			t.Error("the first line had not reached the client five seconds after it was flushed")
		}
		_, _ = io.WriteString(w, "second\n")
	})))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	r := bufio.NewReader(res.Body)
	first, err := r.ReadString('\n')
	if err != nil || first != "first\n" {
		t.Fatalf("first line = %q, %v, want %q", first, err, "first\n")
	}
	close(seen)
	second, err := r.ReadString('\n')
	if err != nil || second != "second\n" {
		t.Errorf("second line = %q, %v, want %q", second, err, "second\n")
	}
}

// plainWriter hides everything but the three methods of an http.ResponseWriter.
type plainWriter struct{ http.ResponseWriter }

// TestNoStoreReportsWhatTheWriterUnderneathCannotDo: a Flush that the writer under it
// cannot do is an error to the caller, not one made up here.
func TestNoStoreReportsWhatTheWriterUnderneathCannotDo(t *testing.T) {
	var got error
	h := webserver.NoStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = http.NewResponseController(w).Flush()
	}))

	h.ServeHTTP(plainWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/", nil))

	if !errors.Is(got, http.ErrNotSupported) {
		t.Errorf("Flush = %v, want an error that is http.ErrNotSupported", got)
	}
}
