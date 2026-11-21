package webserver

import "net/http"

// contentSecurityPolicy lets a page load from its own site and nothing else, and
// run no script at all: the pages have none, and charts are SVG from the server.
// Nothing may frame a page, nor set the base of its links, and a form may only
// send to the site.
const contentSecurityPolicy = "default-src 'self'; script-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Secure sets the security headers of every response of the pages, which is
// personal financial data: the content security policy above, no sniffing of the
// type of content, no referrer sent along a link, no framing, and, above all,
// nothing stored by a browser or a proxy (Cache-Control: no-store).
func Secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
