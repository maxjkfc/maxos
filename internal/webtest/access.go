// Package webtest gives the tests of the pages a stand-in for Cloudflare Access.
//
// Real Access cannot be reached from a test, so a test signs its own tokens: it
// has a key pair of its own, a cfaccess.Verifier that trusts that key and no other,
// and tokens signed with it, which are what Access sends to the origin in the
// Cf-Access-Jwt-Assertion header. The verifier is the product's own, with a fixed
// key set in place of the one fetched from the team domain: nothing in the product
// is switched off to let a test in.
package webtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/maxjkfc/maxos/pkg/cfaccess"
)

const (
	// Team is the team domain the tokens are issued by.
	Team = "https://webtest.cloudflareaccess.com"
	// AUD is the audience of the application the tokens are for.
	AUD = "webtest-aud-tag"
	// Header is the header Cloudflare Access sends the token in.
	Header = "Cf-Access-Jwt-Assertion"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
	keyErr  error
)

// Access is a key pair, the verifier that trusts it, and the means to sign tokens.
type Access struct {
	key      *rsa.PrivateKey
	verifier *cfaccess.Verifier
}

// NewAccess returns a stand-in for Access. The key is made once for the whole
// test binary: making an RSA key takes long enough to notice.
func NewAccess(tb testing.TB) *Access {
	tb.Helper()
	keyOnce.Do(func() { testKey, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		tb.Fatalf("generate the test key: %v", keyErr)
	}
	return &Access{
		key: testKey,
		verifier: cfaccess.NewVerifierWithKeySet(Team, AUD, &oidc.StaticKeySet{
			PublicKeys: []crypto.PublicKey{&testKey.PublicKey},
		}, slog.New(slog.DiscardHandler)),
	}
}

// Verifier returns the verifier that trusts the key of the stand-in.
func (a *Access) Verifier() *cfaccess.Verifier {
	return a.verifier
}

// Token signs a token for the user with the email, valid for an hour.
func (a *Access) Token(tb testing.TB, email string) string {
	tb.Helper()
	return a.sign(tb, map[string]any{
		"aud":   []string{AUD},
		"iss":   Team,
		"sub":   "webtest-" + email,
		"email": email,
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"nbf":   time.Now().Add(-time.Minute).Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
}

// Sign signs any claims with the key of the stand-in, for a test of a token that
// is wrong in some way.
func (a *Access) Sign(tb testing.TB, claims map[string]any) string {
	tb.Helper()
	return a.sign(tb, claims)
}

func (a *Access) sign(tb testing.TB, claims map[string]any) string {
	tb.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: a.key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		tb.Fatalf("make the signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		tb.Fatalf("sign the token: %v", err)
	}
	return raw
}

// Request makes a request as the user with the email, who is signed in: it carries
// a token as Access sends it.
func (a *Access) Request(tb testing.TB, method, target, email string) *http.Request {
	tb.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set(Header, a.Token(tb, email))
	return req
}
