package webserver_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/maxjkfc/maxos/pkg/webserver"
)

var templates = fstest.MapFS{
	"t/hello.gohtml":   {Data: []byte(`{{define "content"}}<p id="greeting">{{.Data}}</p>{{template "badge" "from the shared file"}}{{end}}`)},
	"t/_shared.gohtml": {Data: []byte(`{{define "badge"}}<span class="badge">{{.}}</span>{{end}}`)},
}

// logBuffer collects what the pages log.
type logBuffer struct{ bytes.Buffer }

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, nil))
}

// tokenGuard lets a request through when it carries the header X-Token: ok, the way
// the Access verifier lets one through with a token. What it refuses is answered the
// way the verifier answers.
func tokenGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "ok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func user(*http.Request) (webserver.User, bool) {
	return webserver.User{Name: "max@example.com"}, true
}

// build makes the pages of one registrar that serves what handlers says.
func build(t *testing.T, logs *logBuffer, register func(site *webserver.Site)) *webserver.Pages {
	t.Helper()
	pages, err := webserver.NewRegistry(logs.logger(), []webserver.Registrar{register}).Pages(user, tokenGuard)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	return pages
}

func standard(site *webserver.Site) {
	site.Templates(templates, "t/*.gohtml")
	site.Nav(webserver.NavItem{Order: 20, Key: "second", Label: "第二頁", Href: "/app/second"})
	site.Nav(webserver.NavItem{Order: 10, Key: "hello", Label: "你好", Href: "/app/"})
	site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) {
		return webserver.Page{Name: "hello", Title: "問候", Nav: "hello", Data: "哈囉 <b>world</b>"}, nil
	})
	site.Get("/app/items/{id}", func(r *http.Request) (webserver.Page, error) {
		switch id := r.PathValue("id"); id {
		case "missing":
			return webserver.Page{}, webserver.NotFound("沒有 " + id + " 這個項目")
		case "bad":
			return webserver.Page{}, webserver.BadRequest("參數有誤：<script>alert(1)</script>")
		case "boom":
			return webserver.Page{}, errors.New("connection to db-secret-host refused")
		default:
			return webserver.Page{Name: "hello", Title: id, Data: id}, nil
		}
	})
}

func do(h http.Handler, method, target string, signedIn bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if signedIn {
		req.Header.Set("X-Token", "ok")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func body(rec *httptest.ResponseRecorder) string {
	b, _ := io.ReadAll(rec.Result().Body)
	return string(b)
}

// wantSecure checks the headers of a response of the pages.
func wantSecure(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
		"X-Frame-Options":        "DENY",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s: %s = %q, want %q", what, name, got, value)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "script-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("%s: Content-Security-Policy = %q, want %q in it", what, csp, directive)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "http") {
		t.Errorf("%s: Content-Security-Policy = %q lets in something that is not the site", what, csp)
	}
}

func TestPageIsRenderedInTheLayout(t *testing.T) {
	pages := build(t, &logBuffer{}, standard)
	rec := do(pages, http.MethodGet, "/app/", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	wantSecure(t, "page", rec)
	html := body(rec)
	for _, want := range []string{
		`<html lang="zh-Hant">`,
		`<title>問候・maxos</title>`,
		`<h1>問候</h1>`,
		`href="/app/static/app.css"`,
		`<strong>max@example.com</strong>`,
		// The data of the page is escaped, and the file shared by the pages is there.
		`<p id="greeting">哈囉 &lt;b&gt;world&lt;/b&gt;</p>`,
		`<span class="badge">from the shared file</span>`,
		// The navigation is by order, not by registration, and marks the current page.
		`<a href="/app/" aria-current="page">你好</a>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q:\n%s", want, html)
		}
	}
	if strings.Index(html, "你好") > strings.Index(html, "第二頁") {
		t.Errorf("navigation is not in the order of its entries:\n%s", html)
	}
	if strings.Contains(html, `<a href="/app/second" aria-current`) {
		t.Errorf("a page that is not the current one is marked as current:\n%s", html)
	}
	if strings.Contains(html, "<script") {
		t.Errorf("a page has a script in it:\n%s", html)
	}
}

func TestHeadHasNoBody(t *testing.T) {
	rec := do(build(t, &logBuffer{}, standard), http.MethodHead, "/app/", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	wantSecure(t, "HEAD", rec)
}

func TestErrorsAreShownAsPages(t *testing.T) {
	logs := &logBuffer{}
	pages := build(t, logs, standard)
	tests := []struct {
		name, path   string
		wantStatus   int
		wantInBody   []string
		wantNotInLog string
	}{
		{"not found", "/app/items/missing", http.StatusNotFound, []string{"找不到", "沒有 missing 這個項目", "狀態碼 404"}, ""},
		{"bad request escapes the message", "/app/items/bad", http.StatusBadRequest, []string{"網址或參數有誤", "&lt;script&gt;alert(1)&lt;/script&gt;"}, ""},
		{"unknown path", "/app/nowhere/at/all", http.StatusNotFound, []string{"找不到這個頁面。"}, ""},
		{"unknown path under static", "/app/static/other.css", http.StatusNotFound, []string{"找不到這個頁面。"}, ""},
		{"failure says nothing of the cause", "/app/items/boom", http.StatusInternalServerError, []string{"伺服器發生錯誤"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(pages, http.MethodGet, tt.path, true)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			wantSecure(t, tt.name, rec)
			html := body(rec)
			for _, want := range tt.wantInBody {
				if !strings.Contains(html, want) {
					t.Errorf("page lacks %q:\n%s", want, html)
				}
			}
			if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "db-secret-host") {
				t.Errorf("page shows what it must not:\n%s", html)
			}
			if !strings.Contains(html, "<nav") {
				t.Errorf("an error is not shown in the layout:\n%s", html)
			}
		})
	}
	if !strings.Contains(logs.String(), "db-secret-host") {
		t.Errorf("the cause of the 500 is not logged: %q", logs.String())
	}
	if strings.Count(logs.String(), "level=ERROR") != 1 {
		t.Errorf("only the failure is an error of the log, got:\n%s", logs.String())
	}
}

func TestOnlyReadingMethodsAreServed(t *testing.T) {
	pages := build(t, &logBuffer{}, standard)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace, "PROPFIND"} {
		for _, path := range []string{"/app/", "/app/items/1", "/app/nowhere", "/app/static/app.css", "/app"} {
			t.Run(method+" "+path, func(t *testing.T) {
				rec := do(pages, method, path, true)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Fatalf("status = %d, want 405", rec.Code)
				}
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q, want GET, HEAD", got)
				}
				wantSecure(t, "405", rec)
			})
		}
	}
}

func TestNothingIsServedWithoutTheGuardLettingItThrough(t *testing.T) {
	called := false
	pages := build(t, &logBuffer{}, func(site *webserver.Site) {
		site.Templates(templates, "t/*.gohtml")
		site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) {
			called = true
			return webserver.Page{Name: "hello", Title: "x"}, nil
		})
	})
	for _, route := range pages.Routes() {
		for _, method := range []string{route.Method, http.MethodPost} {
			t.Run(method+" "+route.Pattern, func(t *testing.T) {
				rec := do(pages, method, strings.ReplaceAll(route.Pattern, "{$}", ""), false)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401 for a request without a token", rec.Code)
				}
				if body(rec) != "unauthorized\n" {
					t.Errorf("body = %q, want the guard's own refusal", body(rec))
				}
				// The refusal of the guard carries the headers too.
				wantSecure(t, "401", rec)
			})
		}
	}
	for _, path := range []string{"/app/nowhere", "/app/static/app.css", "/app"} {
		if rec := do(pages, http.MethodGet, path, false); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token: status = %d, want 401", path, rec.Code)
		}
	}
	if called {
		t.Error("a handler ran for a request the guard refused")
	}
}

func TestARequestTheGuardLetsThroughWithoutAUserIsRefused(t *testing.T) {
	called := false
	pages, err := webserver.NewRegistry(slog.New(slog.DiscardHandler), []webserver.Registrar{func(site *webserver.Site) {
		site.Templates(templates, "t/*.gohtml")
		site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) {
			called = true
			return webserver.Page{Name: "hello", Title: "x"}, nil
		})
	}}).Pages(func(*http.Request) (webserver.User, bool) { return webserver.User{}, false }, tokenGuard)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(pages, http.MethodGet, "/app/", true)
	if rec.Code != http.StatusUnauthorized || called {
		t.Errorf("status = %d, handler called = %v: want 401 and no handler", rec.Code, called)
	}
}

func TestHandlersKnowTheUser(t *testing.T) {
	var got webserver.User
	var ok bool
	pages := build(t, &logBuffer{}, func(site *webserver.Site) {
		site.Templates(templates, "t/*.gohtml")
		site.Get("/app/{$}", func(r *http.Request) (webserver.Page, error) {
			got, ok = webserver.UserFrom(r.Context())
			return webserver.Page{Name: "hello", Title: "x"}, nil
		})
	})
	do(pages, http.MethodGet, "/app/", true)
	if !ok || got.Name != "max@example.com" {
		t.Errorf("UserFrom = %+v, %v", got, ok)
	}
}

func TestTheServerRoutes(t *testing.T) {
	pages := build(t, &logBuffer{}, standard)

	css := do(pages, http.MethodGet, "/app/static/app.css", true)
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("stylesheet: status %d, type %q", css.Code, css.Header().Get("Content-Type"))
	}
	wantSecure(t, "stylesheet", css)
	if !strings.Contains(body(css), "prefers-color-scheme") {
		t.Error("the stylesheet is not the stylesheet of the pages")
	}

	redirect := do(pages, http.MethodGet, "/app", true)
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/app/" {
		t.Errorf("/app: status %d to %q, want 301 to /app/", redirect.Code, redirect.Header().Get("Location"))
	}
	wantSecure(t, "redirect", redirect)

	var patterns []string
	for _, route := range pages.Routes() {
		patterns = append(patterns, route.Method+" "+route.Pattern)
	}
	for _, want := range []string{"GET /app/{$}", "GET /app/items/{id}", "GET /app/static/app.css", "GET /app", "GET /app/"} {
		found := false
		for _, p := range patterns {
			found = found || p == want
		}
		if !found {
			t.Errorf("Routes() = %v, lacks %q", patterns, want)
		}
	}
}

func TestPagesNeedAGuardAndAUser(t *testing.T) {
	registry := webserver.NewRegistry(slog.New(slog.DiscardHandler), nil)
	if _, err := registry.Pages(user, nil); err == nil {
		t.Error("Pages without a guard succeeded: they would be served to anyone")
	}
	if _, err := registry.Pages(nil, tokenGuard); err == nil {
		t.Error("Pages without a user function succeeded")
	}
}

func TestWhatCannotBeServedFailsTheBuild(t *testing.T) {
	handler := func(*http.Request) (webserver.Page, error) { return webserver.Page{}, nil }
	tests := []struct {
		name     string
		register func(site *webserver.Site)
	}{
		{"a template that does not parse", func(site *webserver.Site) {
			site.Templates(fstest.MapFS{"bad/x.gohtml": {Data: []byte(`{{define "content"}}{{.Data.}}{{end}}`)}}, "bad/*.gohtml")
		}},
		{"no template at all", func(site *webserver.Site) { site.Templates(templates, "nothing/*.gohtml") }},
		{"two pages of one name", func(site *webserver.Site) {
			site.Templates(templates, "t/*.gohtml")
			site.Templates(templates, "t/*.gohtml")
		}},
		{"a route outside the prefix", func(site *webserver.Site) { site.Get("/elsewhere", handler) }},
		{"a route that clashes with another", func(site *webserver.Site) {
			site.Get("/app/x", handler)
			site.Get("/app/x", handler)
		}},
		{"an entry of the navigation outside the prefix", func(site *webserver.Site) {
			site.Nav(webserver.NavItem{Key: "k", Label: "L", Href: "https://example.com/"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := webserver.NewRegistry(slog.New(slog.DiscardHandler), []webserver.Registrar{tt.register}).Pages(user, tokenGuard)
			if err == nil {
				t.Fatal("Pages succeeded")
			}
		})
	}
}

func TestAHandlerThatNamesNoPageIsAServerError(t *testing.T) {
	logs := &logBuffer{}
	pages := build(t, logs, func(site *webserver.Site) {
		site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) { return webserver.Page{Name: "nope", Title: "x"}, nil })
	})
	rec := do(pages, http.MethodGet, "/app/", true)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(logs.String(), "nope") {
		t.Errorf("the page that is not registered is not logged: %q", logs.String())
	}
}

func TestATemplateThatFailsHalfWayGivesNoHalfPage(t *testing.T) {
	broken := fstest.MapFS{"b/broken.gohtml": {Data: []byte(`{{define "content"}}<p>before</p>{{.Data.Missing.Deeper}}<p>after</p>{{end}}`)}}
	logs := &logBuffer{}
	pages := build(t, logs, func(site *webserver.Site) {
		site.Templates(broken, "b/*.gohtml")
		site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) {
			return webserver.Page{Name: "broken", Title: "x", Data: struct{}{}}, nil
		})
	})
	rec := do(pages, http.MethodGet, "/app/", true)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(body(rec), "before") {
		t.Errorf("a page that failed half way was written in part: %q", body(rec))
	}
}

func TestAnErrorFromAHandlerIsWrapped(t *testing.T) {
	// A handler may wrap an *Error with what it was doing; the page is still its.
	pages := build(t, &logBuffer{}, func(site *webserver.Site) {
		site.Get("/app/{$}", func(*http.Request) (webserver.Page, error) {
			return webserver.Page{}, errors.Join(errors.New("while reading"), webserver.NotFound("沒有這個"))
		})
	})
	if rec := do(pages, http.MethodGet, "/app/", true); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
