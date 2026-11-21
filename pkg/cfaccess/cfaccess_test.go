package cfaccess

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testTeam = "https://myteam.cloudflareaccess.com"
	testAUD  = "test-aud-tag"
)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMiddleware(t *testing.T) {
	goodKey := newKey(t)
	otherKey := newKey(t)
	now := time.Now()

	claims := func(override map[string]any) map[string]any {
		c := map[string]any{
			"aud":   []string{testAUD},
			"iss":   testTeam,
			"sub":   "7335d417-61da-459d-899c-0a01c76a2f94",
			"email": "user@example.com",
			"iat":   now.Add(-time.Minute).Unix(),
			"nbf":   now.Add(-time.Minute).Unix(),
			"exp":   now.Add(time.Hour).Unix(),
		}
		for k, v := range override {
			if v == nil {
				delete(c, k)
			} else {
				c[k] = v
			}
		}
		return c
	}

	tests := []struct {
		name       string
		header     func(t *testing.T) string
		wantStatus int
		wantActor  string // exact Actor(), or prefix when wantPrefix is set
		wantPrefix bool
		wantEmail  string
	}{
		{
			name:       "valid user token",
			header:     func(t *testing.T) string { return sign(t, goodKey, claims(nil)) },
			wantStatus: http.StatusOK,
			wantActor:  "user@example.com",
			wantEmail:  "user@example.com",
		},
		{
			name: "valid service token",
			header: func(t *testing.T) string {
				return sign(t, goodKey, claims(map[string]any{
					"email":       nil,
					"sub":         "",
					"common_name": "e367826f93b8d71185e03fe518aff3b4.access",
				}))
			},
			wantStatus: http.StatusOK,
			wantActor:  "service:",
			wantPrefix: true,
		},
		{
			name:       "missing header",
			header:     func(t *testing.T) string { return "" },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "garbage token",
			header:     func(t *testing.T) string { return "not-a-jwt" },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong audience",
			header: func(t *testing.T) string {
				return sign(t, goodKey, claims(map[string]any{"aud": []string{"other-aud"}}))
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong issuer",
			header: func(t *testing.T) string {
				return sign(t, goodKey, claims(map[string]any{"iss": "https://evil.cloudflareaccess.com"}))
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "expired token",
			header: func(t *testing.T) string {
				return sign(t, goodKey, claims(map[string]any{
					"iat": now.Add(-2 * time.Hour).Unix(),
					"nbf": now.Add(-2 * time.Hour).Unix(),
					"exp": now.Add(-time.Hour).Unix(),
				}))
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "signed by different key",
			header:     func(t *testing.T) string { return sign(t, otherKey, claims(nil)) },
			wantStatus: http.StatusUnauthorized,
		},
	}

	// Trailing slash on the team domain must be normalised away.
	v := NewVerifierWithKeySet(testTeam+"/", testAUD, &oidc.StaticKeySet{
		PublicKeys: []crypto.PublicKey{&goodKey.PublicKey},
	}, slog.New(slog.DiscardHandler))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var gotID Identity
			var gotOK bool
			h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotID, gotOK = IdentityFrom(r.Context())
			}))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			raw := tt.header(t)
			if raw != "" {
				req.Header.Set(assertionHeader, raw)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if called {
					t.Fatal("next handler was called for a rejected request")
				}
				if raw != "" && strings.Contains(rec.Body.String(), raw) {
					t.Fatal("response body echoes the token")
				}
				return
			}
			if !called || !gotOK {
				t.Fatal("next handler not called with an Identity in context")
			}
			if gotID.Email != tt.wantEmail {
				t.Errorf("Email = %q, want %q", gotID.Email, tt.wantEmail)
			}
			actor := gotID.Actor()
			if tt.wantPrefix {
				if !strings.HasPrefix(actor, tt.wantActor) || actor == tt.wantActor {
					t.Errorf("Actor() = %q, want prefix %q followed by an id", actor, tt.wantActor)
				}
			} else if actor != tt.wantActor {
				t.Errorf("Actor() = %q, want %q", actor, tt.wantActor)
			}
		})
	}
}

// A JWKS endpoint that accepts the request and never answers must make
// verification fail once the fetch timeout passes, not hang. The request
// context here has no deadline: only the key-fetch client can end the wait.
func TestStalledJWKSFailsWithinTimeout(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	// Cleanups run last-in first-out: release the stalled handler before Close waits for it.
	t.Cleanup(srv.Close)
	t.Cleanup(releaseAll)

	const timeout = 200 * time.Millisecond
	v, err := newVerifier(srv.URL, testAUD, slog.New(slog.DiscardHandler), &http.Client{Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}

	key := newKey(t)
	now := time.Now()
	raw := sign(t, key, map[string]any{
		"aud": []string{testAUD},
		"iss": srv.URL,
		"sub": "user",
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(assertionHeader, raw)

	done := make(chan int, 1)
	start := time.Now()
	go func() {
		rec := httptest.NewRecorder()
		v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler was called although the keys could not be fetched")
		})).ServeHTTP(rec, req)
		done <- rec.Code
	}()

	select {
	case code := <-done:
		if code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", code, http.StatusUnauthorized)
		}
		if elapsed := time.Since(start); elapsed < timeout {
			t.Fatalf("failed after %v, before the %v fetch timeout", elapsed, timeout)
		}
	case <-time.After(10 * timeout):
		t.Fatalf("verification still blocked after %v; the JWKS fetch has no effective timeout", 10*timeout)
	}
}

// rejected sends a request with the path and the token (if there is one) to a
// verifier, which rejects it, and returns the record that it wrote to the log and the
// length of the log. The verifier trusts no key: the tokens of these tests are not
// the ones it checks the signature of.
func rejected(t *testing.T, path, token string) (record map[string]any, logLen int) {
	t.Helper()
	var buf bytes.Buffer
	v := NewVerifierWithKeySet(testTeam, testAUD, &oidc.StaticKeySet{}, slog.New(slog.NewJSONHandler(&buf, nil)))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.URL.Path = path
	if token != "" {
		req.Header.Set(assertionHeader, token)
	}
	rec := httptest.NewRecorder()

	v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler was called for a rejected request")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("the log is not one record: %v", err)
	}
	return record, buf.Len()
}

// TestARejectedPathIsLoggedCapped: the client chooses the path, and a server takes a
// megabyte of headers, so a rejected request must not put all of it in the log: the
// first 200 characters, an ellipsis, and how long the path was. A path that is not
// longer is logged as it is.
func TestARejectedPathIsLoggedCapped(t *testing.T) {
	const kilobyte = 1 << 10
	tests := []struct {
		name      string
		path      string
		want      string // the path as logged
		wantBytes int    // the length in bytes that the record gives, or 0 when it does not
		maxLog    int    // the most that the whole log may hold
	}{
		{name: "a short path as it is", path: "/mcp", want: "/mcp", maxLog: kilobyte},
		{name: "200 characters as they are", path: "/" + strings.Repeat("a", 199), want: "/" + strings.Repeat("a", 199), maxLog: kilobyte},
		{
			name: "201 characters cut after 200", path: "/" + strings.Repeat("a", 200),
			want: "/" + strings.Repeat("a", 199) + "…", wantBytes: 201, maxLog: kilobyte,
		},
		{
			name: "900 KB", path: "/" + strings.Repeat("a", 900*kilobyte),
			want: "/" + strings.Repeat("a", 199) + "…", wantBytes: 1 + 900*kilobyte, maxLog: kilobyte,
		},
		{
			// A byte count would cut the third byte of the 67th character here.
			name: "characters of three bytes", path: "/" + strings.Repeat("路", 300),
			want: "/" + strings.Repeat("路", 199) + "…", wantBytes: 1 + 300*3, maxLog: 2 * kilobyte,
		},
		{
			name: "characters of four bytes", path: "/" + strings.Repeat("😀", 300),
			want: "/" + strings.Repeat("😀", 199) + "…", wantBytes: 1 + 300*4, maxLog: 2 * kilobyte,
		},
		{
			// A byte that is no character is one of the 200 like any other.
			name: "bytes that are not UTF-8", path: strings.Repeat("\xff", 300),
			want: strings.Repeat("\ufffd", 200) + "…", wantBytes: 300, maxLog: 2 * kilobyte,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, logLen := rejected(t, tt.path, "")

			if got := record["path"]; got != tt.want {
				t.Errorf("path = %.60q (%d bytes), want %.60q (%d bytes)", got, len(got.(string)), tt.want, len(tt.want))
			}
			if tt.wantBytes == 0 {
				if got, ok := record["path_bytes"]; ok {
					t.Errorf("path_bytes = %v on a path that was not cut", got)
				}
			} else if got, _ := record["path_bytes"].(float64); int(got) != tt.wantBytes {
				t.Errorf("path_bytes = %v, want %d", record["path_bytes"], tt.wantBytes)
			}
			if logLen > tt.maxLog {
				t.Errorf("the log of one rejected request is %d bytes, want at most %d", logLen, tt.maxLog)
			}
		})
	}
}

// TestARejectedTokenIsLoggedCapped: the reason that a token is rejected for is in the
// log too, and go-jose repeats in it the algorithm that the header of the token names,
// which is as long as the client likes and needs no key to be signed with. The reason
// is capped as the path is.
func TestARejectedTokenIsLoggedCapped(t *testing.T) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"` + strings.Repeat("A", 500<<10) + `","typ":"JWT"}`))
	token := header + "." + enc.EncodeToString([]byte(`{}`)) + "." + enc.EncodeToString([]byte("signature"))

	record, logLen := rejected(t, "/mcp", token)

	reason, _ := record["reason"].(string)
	if !strings.HasSuffix(reason, "…") || utf8.RuneCountInString(reason) != maxLoggedRunes+1 {
		t.Errorf("reason = %.80q (%d bytes), want %d characters and an ellipsis: this test needs a reason that the client makes long", reason, len(reason), maxLoggedRunes)
	}
	if got, _ := record["reason_bytes"].(float64); got < 500<<10 {
		t.Errorf("reason_bytes = %v, want the length of the reason, at least %d", record["reason_bytes"], 500<<10)
	}
	if logLen > 2<<10 {
		t.Errorf("the log of one rejected request is %d bytes, want at most %d", logLen, 2<<10)
	}
}

func TestClip(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		limit   int
		want    string
		wantCut bool
	}{
		{"empty", "", 3, "", false},
		{"shorter", "ab", 3, "ab", false},
		{"as long", "abc", 3, "abc", false},
		{"one more", "abcd", 3, "abc…", true},
		{"many more", strings.Repeat("a", 1000), 3, "aaa…", true},
		{"as many characters as the limit, more bytes", "路路路", 3, "路路路", false},
		{"more characters, cut between them", "路路路路", 3, "路路路…", true},
		{"four bytes each", "😀😀😀😀", 2, "😀😀…", true},
		{"bytes that are not UTF-8 are one each", "ab\xe8\xb7", 3, "ab\xe8…", true},
		{"nothing is kept", "abc", 0, "…", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := clip(tt.s, tt.limit)
			if got != tt.want || cut != tt.wantCut {
				t.Errorf("clip(%q, %d) = %q, %v, want %q, %v", tt.s, tt.limit, got, cut, tt.want, tt.wantCut)
			}
		})
	}
}
