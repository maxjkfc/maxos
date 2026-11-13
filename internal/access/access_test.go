package access

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	})

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
