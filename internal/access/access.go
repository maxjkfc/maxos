// Package access verifies the Cloudflare Access application token that
// Cloudflare sends to the origin in the Cf-Access-Jwt-Assertion header.
package access

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	assertionHeader = "Cf-Access-Jwt-Assertion"
	certsPath       = "/cdn-cgi/access/certs"
)

// Identity is the authenticated caller extracted from an Access token.
type Identity struct {
	// Subject is the token "sub" for user tokens, or the service token
	// Client ID ("common_name") for service tokens, whose "sub" is empty.
	Subject string
	// Email is empty for service tokens.
	Email string
}

// Actor returns the email when present, otherwise "service:" plus the subject.
func (id Identity) Actor() string {
	if id.Email != "" {
		return id.Email
	}
	return "service:" + id.Subject
}

type ctxKey struct{}

// IdentityFrom returns the Identity stored by Verifier.Middleware.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Verifier validates Cloudflare Access application tokens.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewVerifier builds a Verifier that fetches signing keys from
// teamDomain + "/cdn-cgi/access/certs". Both arguments are required so the
// server cannot start unauthenticated.
func NewVerifier(ctx context.Context, teamDomain, aud string) (*Verifier, error) {
	teamDomain = normalizeDomain(teamDomain)
	if teamDomain == "" || aud == "" {
		return nil, errors.New("access: team domain and audience are both required")
	}
	return NewVerifierWithKeySet(teamDomain, aud, oidc.NewRemoteKeySet(ctx, teamDomain+certsPath)), nil
}

// NewVerifierWithKeySet is like NewVerifier but takes an explicit key set.
func NewVerifierWithKeySet(teamDomain, aud string, ks oidc.KeySet) *Verifier {
	return &Verifier{
		verifier: oidc.NewVerifier(normalizeDomain(teamDomain), ks, &oidc.Config{
			ClientID:             aud,
			SupportedSigningAlgs: []string{oidc.RS256},
		}),
	}
}

func normalizeDomain(d string) string {
	return strings.TrimRight(strings.TrimSpace(d), "/")
}

// Middleware rejects requests without a valid Access token with 401 and
// otherwise stores the caller Identity in the request context. Only the
// header is honoured; the CF_Authorization cookie is deliberately ignored.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := v.identify(r)
		if err != nil {
			// The reason is logged server-side only; neither the token nor the
			// verification detail is returned to the client.
			slog.Warn("access: request rejected", "reason", err.Error(), "path", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func (v *Verifier) identify(r *http.Request) (Identity, error) {
	raw := r.Header.Get(assertionHeader)
	if raw == "" {
		return Identity{}, errors.New("missing " + assertionHeader + " header")
	}
	tok, err := v.verifier.Verify(r.Context(), raw)
	if err != nil {
		return Identity{}, err
	}
	var claims struct {
		Email      string `json:"email"`
		CommonName string `json:"common_name"`
	}
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, err
	}
	id := Identity{Subject: tok.Subject, Email: claims.Email}
	if id.Subject == "" {
		id.Subject = claims.CommonName
	}
	if id.Email == "" && id.Subject == "" {
		return Identity{}, errors.New("token has neither email nor subject")
	}
	return id, nil
}
