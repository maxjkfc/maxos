// Package webserver serves the server-rendered pages of maxos under /app/.
//
// It knows nothing about any bounded context: each context contributes a
// Registrar that adds its own pages to a Site (a route, the templates of the page,
// an entry of the navigation), and a page acts on behalf of the user the guard
// identified. It is to the pages what pkg/mcpserver is to the MCP tools.
//
// What it guarantees, whatever a context registers:
//   - Every route, the stylesheet and the answers to unknown paths and methods
//     are behind the guard (the Cloudflare Access verifier): there is no way to
//     build the pages without one, and no switch to turn it off.
//   - Every response carries the security headers (see Secure).
//   - Only GET (and HEAD) is served; any other method is 405. A write action later
//     is a Post beside Get: it is where the check of the origin and of a token
//     (CSRF) goes, in the one place that method is let through (see Site.methods),
//     and no page needs to change.
//   - A handler returns the page to show or an error; an *Error becomes its status
//     with a page that says what is wrong, anything else a 500 page that says
//     nothing of the cause (it is logged).
package webserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Prefix is where the pages are served from. Every route starts with it.
const Prefix = "/app"

// StylesheetPath is where the one stylesheet of the pages is served from.
const StylesheetPath = Prefix + "/static/app.css"

// User is who is signed in, as the header of a page shows them.
type User struct {
	// Name is the email of the signed-in user, or the identity of a service token.
	Name string
}

// UserFunc identifies the user of an HTTP request that the guard let through. It
// reports false when it cannot, and the request is then refused as the guard
// refuses one it cannot identify.
type UserFunc func(r *http.Request) (User, bool)

type userKey struct{}

// UserFrom returns the user a handler is serving. A write action later attributes
// what it writes to them.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// Guard wraps a handler so that a request is served only when it is allowed to be:
// the Cloudflare Access verifier's Middleware.
type Guard func(next http.Handler) http.Handler

// Page is what a handler wants shown.
type Page struct {
	// Name is the page template to render, as Site.Templates named it: the file
	// name without its extension.
	Name string
	// Title is the title of the page: the title of the browser tab and the heading.
	Title string
	// Nav is the Key of the NavItem to mark as the current one; empty marks none.
	Nav string
	// Data is what the template sees as .Data.
	Data any
}

// Handler makes the page of a request, or says why it cannot. An *Error is shown
// with its status; any other error is a 500.
type Handler func(r *http.Request) (Page, error)

// NavItem is an entry of the navigation at the top of every page.
type NavItem struct {
	// Order sorts the entries, lowest first, then by Key; contexts agree on it
	// among themselves.
	Order int
	// Key identifies the entry; a Page whose Nav is the Key marks it as current.
	Key   string
	Label string
	// Href is a path under Prefix.
	Href string
}

// Route is a route of the pages: a method and the pattern of its path.
type Route struct {
	Method  string
	Pattern string
}

// Registrar adds one context's pages to site.
type Registrar func(site *Site)

// Registry builds the pages of every registrar.
type Registry struct {
	logger     *slog.Logger
	registrars []Registrar
}

// NewRegistry returns a Registry whose pages report what goes wrong inside them to
// logger.
func NewRegistry(logger *slog.Logger, registrars []Registrar) *Registry {
	return &Registry{logger: logger, registrars: registrars}
}

// Pages builds the pages: it runs every registrar, and puts the guard in front of
// all of them. userOf names the user of a request the guard let through. Both are
// required; the pages are never served without them. It fails when a registrar
// registers something that cannot be served (a template that does not parse, two
// pages of one name, a route that clashes with another).
func (r *Registry) Pages(userOf UserFunc, guard Guard) (*Pages, error) {
	if userOf == nil || guard == nil {
		return nil, errors.New("webserver: the pages need a guard and a user function: they are never served without them")
	}
	layout, err := parseLayout()
	if err != nil {
		return nil, err
	}
	site := &Site{
		logger: r.logger,
		mux:    http.NewServeMux(),
		layout: layout,
		pages:  make(map[string]*template.Template),
		userOf: userOf,
	}
	site.addErrorPage()
	for _, register := range r.registrars {
		register(site)
	}

	site.handle("GET", Prefix+"/static/app.css", serveStylesheet)
	site.handle("GET", Prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Prefix+"/", http.StatusMovedPermanently)
	})
	// The pattern that has the fewest paths of its own: every path no page claims.
	site.handle("GET", Prefix+"/", site.page(func(*http.Request) (Page, error) {
		return Page{}, NotFound("找不到這個頁面。")
	}))
	if err := errors.Join(site.errs...); err != nil {
		return nil, err
	}

	slices.SortStableFunc(site.nav, func(a, b NavItem) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Key, b.Key)
	})
	return &Pages{
		handler: Secure(guard(site.identify(site.methods(site.mux)))),
		routes:  site.routes,
	}, nil
}

// Pages is the handler of every page, and the routes it has.
type Pages struct {
	handler http.Handler
	routes  []Route
}

// ServeHTTP serves a request for a page.
func (p *Pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.handler.ServeHTTP(w, r)
}

// Routes returns the routes of the pages, the ones the registrars added and the
// ones of the server itself: a test that goes over every route starts from them.
func (p *Pages) Routes() []Route {
	return slices.Clone(p.routes)
}

// Site is what a Registrar adds its pages to.
type Site struct {
	logger *slog.Logger
	mux    *http.ServeMux
	layout *template.Template
	pages  map[string]*template.Template
	nav    []NavItem
	routes []Route
	userOf UserFunc
	errs   []error
}

// Nav adds an entry to the navigation.
func (s *Site) Nav(item NavItem) {
	if !strings.HasPrefix(item.Href, Prefix) {
		s.errs = append(s.errs, fmt.Errorf("webserver: navigation entry %q goes to %q, which is not under %s", item.Key, item.Href, Prefix))
		return
	}
	s.nav = append(s.nav, item)
}

// Templates parses the page templates that match pattern in fsys. A file is a page
// named after the file name without its extension and defines the template
// "content", which is shown in the layout of the page; what the template sees as
// "." is the layout's data, so the page's own data is .Data. A file whose name
// starts with "_" is not a page but templates that every page of the call may use.
func (s *Site) Templates(fsys fs.FS, pattern string) {
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		s.errs = append(s.errs, fmt.Errorf("webserver: templates %q: %w", pattern, err))
		return
	}
	if len(files) == 0 {
		s.errs = append(s.errs, fmt.Errorf("webserver: no template matches %q", pattern))
		return
	}
	var shared, pages []string
	for _, file := range files {
		if strings.HasPrefix(path.Base(file), "_") {
			shared = append(shared, file)
		} else {
			pages = append(pages, file)
		}
	}
	for _, file := range pages {
		name := strings.TrimSuffix(path.Base(file), path.Ext(file))
		if _, taken := s.pages[name]; taken {
			s.errs = append(s.errs, fmt.Errorf("webserver: two pages are called %q", name))
			continue
		}
		t, err := s.layout.Clone()
		if err == nil {
			t, err = t.ParseFS(fsys, append(slices.Clone(shared), file)...)
		}
		if err != nil {
			s.errs = append(s.errs, fmt.Errorf("webserver: template %s: %w", file, err))
			continue
		}
		s.pages[name] = t
	}
}

// Get serves the page the handler makes at the pattern, to GET and HEAD requests.
// The pattern is a path pattern of net/http (wildcards as {id} included) that
// starts with Prefix; r.PathValue gives a wildcard to the handler.
func (s *Site) Get(pattern string, h Handler) {
	if pattern != Prefix && !strings.HasPrefix(pattern, Prefix+"/") {
		s.errs = append(s.errs, fmt.Errorf("webserver: route %q is not under %s", pattern, Prefix))
		return
	}
	s.handle("GET", pattern, s.page(h))
}

// handle registers a route, and turns the panic of net/http for a pattern that
// clashes with another into an error of the build.
func (s *Site) handle(method, pattern string, h http.HandlerFunc) {
	defer func() {
		if p := recover(); p != nil {
			s.errs = append(s.errs, fmt.Errorf("webserver: route %s %s: %v", method, pattern, p))
		}
	}()
	s.mux.HandleFunc(method+" "+pattern, h)
	s.routes = append(s.routes, Route{Method: method, Pattern: pattern})
}

// identify gives the user of the request to what is behind it, or refuses the
// request as the guard refuses one it cannot identify. The guard has let it through
// and says who it is; a request it let through without saying is a wiring mistake,
// and is not served under an anonymous user.
func (s *Site) identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.userOf(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	})
}

// methods lets the methods that only read through, and answers any other with 405.
//
// It is the one place that decides which methods a request may have, so it is
// where a write action will be let in: a route registered for POST, and in front of
// it the check that the request came from this site (the Origin and Sec-Fetch-Site
// headers) and carries the token of the form, which is one middleware for every
// write and not a check in each handler.
func (s *Site) methods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD")
			s.renderError(w, r, &Error{Status: http.StatusMethodNotAllowed, Message: "這裡只能讀取，不能用這個方法操作。"})
		}
	})
}

// page makes the route of a handler: it runs it, and shows what it returns.
func (s *Site) page(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := h(r)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		s.render(w, r, http.StatusOK, page, page.Data)
	}
}

// layoutData is what the layout, and with it the template of a page, sees.
type layoutData struct {
	Title  string
	Prefix string
	Style  string
	Nav    []navView
	User   User
	// Data is the data of the page.
	Data any
}

type navView struct {
	NavItem
	Current bool
}

// render writes the page with the status. It is rendered whole before anything is
// written, so that a template that fails half way gives a 500 and not half a page.
func (s *Site) render(w http.ResponseWriter, r *http.Request, status int, page Page, data any) {
	tmpl, ok := s.pages[page.Name]
	if !ok {
		s.logger.Error("web: a handler asked for a page that is not registered", "page", page.Name, "path", r.URL.Path)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	user, _ := UserFrom(r.Context())
	view := layoutData{Title: page.Title, Prefix: Prefix, Style: StylesheetPath, User: user, Data: data}
	for _, item := range s.nav {
		view.Nav = append(view.Nav, navView{NavItem: item, Current: item.Key == page.Nav})
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", view); err != nil {
		s.logger.Error("web: render failed", "page", page.Name, "path", r.URL.Path, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderError shows err: an *Error as its status and message, anything else as a
// 500 that does not say what went wrong.
func (s *Site) renderError(w http.ResponseWriter, r *http.Request, err error) {
	var webErr *Error
	if !errors.As(err, &webErr) {
		if !errors.Is(err, context.Canceled) {
			s.logger.Error("web: request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		}
		webErr = &Error{Status: http.StatusInternalServerError, Message: "伺服器發生錯誤，請稍後再試。"}
	}
	heading := errorHeading(webErr.Status)
	s.render(w, r, webErr.Status, Page{Name: errorPage, Title: heading}, errorView{Status: webErr.Status, Heading: heading, Message: webErr.Message})
}

// Error is what a handler returns to answer with a status other than 200 and a
// message for the user. The message is shown as it is, so it never has more in it
// than the user may know.
type Error struct {
	// Status is the HTTP status.
	Status int
	// Message says what is wrong, in the language of the pages.
	Message string
	// Err is the cause, which is not shown; it is for errors.Is and errors.As.
	Err error
}

// Error implements error.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, e.Message, e.Err)
	}
	return fmt.Sprintf("%d %s", e.Status, e.Message)
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

// NotFound is the error of a page that is not there.
func NotFound(message string) error {
	return &Error{Status: http.StatusNotFound, Message: message}
}

// BadRequest is the error of a request that is not right: a parameter that does not
// read.
func BadRequest(message string) error {
	return &Error{Status: http.StatusBadRequest, Message: message}
}

const errorPage = "error"

type errorView struct {
	Status  int
	Heading string
	Message string
}

func errorHeading(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "網址或參數有誤"
	case http.StatusNotFound:
		return "找不到"
	case http.StatusMethodNotAllowed:
		return "不能這樣操作"
	default:
		return "發生錯誤"
	}
}
