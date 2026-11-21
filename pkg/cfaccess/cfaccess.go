// Package cfaccess verifies the Cloudflare Access application token that
// Cloudflare sends to the origin in the Cf-Access-Jwt-Assertion header.
package cfaccess

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	assertionHeader = "Cf-Access-Jwt-Assertion"
	certsPath       = "/cdn-cgi/access/certs"

	// jwksTimeout bounds one fetch of the signing keys. go-oidc keeps fetching
	// in a goroutine that ignores context cancellation, so a stalled endpoint
	// would otherwise hold that fetch (and every request waiting on it) forever.
	jwksTimeout = 10 * time.Second
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
	logger   *slog.Logger
}

// NewVerifier builds a Verifier that fetches signing keys from
// teamDomain + "/cdn-cgi/access/certs", giving up on a fetch after 10 seconds.
// Both arguments are required so the server cannot start unauthenticated.
// Rejected requests are logged to logger.
func NewVerifier(teamDomain, aud string, logger *slog.Logger) (*Verifier, error) {
	return newVerifier(teamDomain, aud, logger, &http.Client{Timeout: jwksTimeout})
}

// newVerifier is NewVerifier with the client that fetches the keys. go-oidc
// ignores the cancellation of the context given to NewRemoteKeySet and takes
// only its values, so the client's timeout is the sole bound on a fetch.
func newVerifier(teamDomain, aud string, logger *slog.Logger, client *http.Client) (*Verifier, error) {
	teamDomain = normalizeDomain(teamDomain)
	if teamDomain == "" || aud == "" {
		return nil, errors.New("access: team domain and audience are both required")
	}
	ctx := oidc.ClientContext(context.Background(), client)
	return NewVerifierWithKeySet(teamDomain, aud, oidc.NewRemoteKeySet(ctx, teamDomain+certsPath), logger), nil
}

// NewVerifierWithKeySet is like NewVerifier but takes an explicit key set.
func NewVerifierWithKeySet(teamDomain, aud string, ks oidc.KeySet, logger *slog.Logger) *Verifier {
	return &Verifier{
		verifier: oidc.NewVerifier(normalizeDomain(teamDomain), ks, &oidc.Config{
			ClientID:             aud,
			SupportedSigningAlgs: []string{oidc.RS256},
		}),
		logger: logger,
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
			// verification detail is returned to the client. The reason and the path
			// are what the client chose, a megabyte of it if it likes, so the record
			// carries only the start of them.
			v.logger.Warn("access: request rejected",
				slices.Concat(clipped("reason", err.Error()), clipped("path", r.URL.Path))...)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// maxLoggedRunes is how many characters of something that the client chose a record
// of the log carries. A server takes a megabyte of headers, so the path of a request
// and the reason its token is rejected for (go-jose repeats in it the algorithm that
// the token names) are as long as the client likes.
const maxLoggedRunes = 200

// clipped is the attributes of a record of the log for something that the client
// chose: value under key, as it is, or, when it has more than maxLoggedRunes
// characters, the start of it and an ellipsis, and the length that it had, in bytes,
// under key+"_bytes".
func clipped(key, value string) []any {
	shown, cut := clip(value, maxLoggedRunes)
	if !cut {
		return []any{key, value}
	}
	return []any{key, shown, key + "_bytes", len(value)}
}

// clip returns the first limit characters (runes) of s and an ellipsis, and true,
// when s has more than limit of them; otherwise it returns s and false. It cuts
// between two characters and reads no more of s than it keeps.
func clip(s string, limit int) (string, bool) {
	if len(s) <= limit { // a character takes at least one byte
		return s, false
	}
	n := 0
	for i := range s { // i is where a character starts
		if n == limit {
			return s[:i] + "…", true
		}
		n++
	}
	return s, false
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
