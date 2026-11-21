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

// noStoreCacheControl is what NoStore sets: nothing of the response is stored, by a
// browser or by a proxy, and, as the response may be a stream of events, nothing of
// it is buffered or changed on its way (no-transform).
const noStoreCacheControl = "no-store, no-transform"

// NoStore answers with Cache-Control: no-store, no-transform whatever next sets for
// itself. Secure sets no-store before a handler runs, and a handler that sets the
// header too has the last word: the MCP SDK sets "no-cache, no-transform" on the
// answer to a call, which a browser or a proxy may keep. NoStore sets the header
// again as the response is committed: by WriteHeader, by the first Write, by a
// Flush, or by net/http when next returns without having written. No handler can
// change a header after that. No other header is touched.
//
// The writer that next is given has an Unwrap, so that an http.ResponseController
// reaches the writer under it: the SDK flushes each event of a stream through one.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nw := &noStoreWriter{ResponseWriter: w}
		next.ServeHTTP(nw, r)
		// A response that next wrote nothing to is committed by net/http as next
		// returns, with the headers as they stand.
		nw.commit()
	})
}

// noStoreWriter sets the Cache-Control of a response once, as its headers are
// committed.
type noStoreWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *noStoreWriter) commit() {
	if !w.committed {
		w.committed = true
		w.Header().Set("Cache-Control", noStoreCacheControl)
	}
}

func (w *noStoreWriter) WriteHeader(status int) {
	w.commit()
	w.ResponseWriter.WriteHeader(status)
}

func (w *noStoreWriter) Write(p []byte) (int, error) {
	w.commit()
	return w.ResponseWriter.Write(p)
}

// FlushError commits the headers, as a Flush that comes before any write does, and
// then flushes the writer underneath. An http.ResponseController calls it rather
// than reaching the flush of that writer through Unwrap.
func (w *noStoreWriter) FlushError() error {
	w.commit()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets an http.ResponseController reach the writer underneath for what this
// one has no method for: the deadlines of the connection, hijacking.
func (w *noStoreWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
