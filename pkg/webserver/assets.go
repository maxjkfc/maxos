package webserver

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
)

// The layout, the page that shows an error, and the one stylesheet of the pages are
// part of the binary: the pages load nothing from anywhere else (see Secure).

//go:embed assets/layout.gohtml assets/error.gohtml
var templatesFS embed.FS

//go:embed assets/app.css
var stylesheet []byte

// parseLayout parses the layout, which every page is shown in.
func parseLayout() (*template.Template, error) {
	t, err := template.New("layout-set").ParseFS(templatesFS, "assets/layout.gohtml")
	if err != nil {
		return nil, fmt.Errorf("webserver: layout: %w", err)
	}
	return t, nil
}

// addErrorPage registers the page that shows an Error.
func (s *Site) addErrorPage() {
	t, err := s.layout.Clone()
	if err == nil {
		t, err = t.ParseFS(templatesFS, "assets/error.gohtml")
	}
	if err != nil {
		s.errs = append(s.errs, fmt.Errorf("webserver: error page: %w", err))
		return
	}
	s.pages[errorPage] = t
}

// serveStylesheet serves the stylesheet.
func serveStylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(stylesheet)))
	_, _ = w.Write(stylesheet)
}
