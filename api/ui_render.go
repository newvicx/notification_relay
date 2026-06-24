package api

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed ui_templates_html/*.html
var uiTemplatesFS embed.FS

//go:embed ui_static/*
var uiStaticFS embed.FS

// uiPages are rendered with the shared layout. Each entry is parsed as its
// own *template.Template (layout.html + the page file) to avoid the classic
// html/template gotcha where multiple files in one set can't each define a
// block of the same name ("content").
var uiPages = []string{
	"login.html",
	"events_list.html",
	"event_detail.html",
	"sync_groups.html",
	"templates_list.html",
	"template_form.html",
}

// uiFragments are small htmx-swapped snippets rendered without the layout.
var uiFragments = []string{
	"deliveries_fragment.html",
}

// uiPageData is the data passed to every layout-wrapped page template.
type uiPageData struct {
	Title      string
	User       *User
	IsAdmin    bool
	Flash      string
	FlashClass string
	Data       any
}

func mustLoadUITemplates() (map[string]*template.Template, map[string]*template.Template) {
	pages := make(map[string]*template.Template, len(uiPages))
	for _, p := range uiPages {
		t := template.Must(template.New("layout.html").ParseFS(uiTemplatesFS,
			"ui_templates_html/layout.html", "ui_templates_html/"+p))
		pages[p] = t
	}
	fragments := make(map[string]*template.Template, len(uiFragments))
	for _, f := range uiFragments {
		t := template.Must(template.New(f).ParseFS(uiTemplatesFS, "ui_templates_html/"+f))
		fragments[f] = t
	}
	return pages, fragments
}

func (s *Server) renderUIPage(w http.ResponseWriter, page string, data uiPageData) {
	tmpl, ok := s.uiPages[page]
	if !ok {
		s.logger.Error("renderUIPage: unknown page", "page", page)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("renderUIPage: execute failed", "page", page, "error", err)
	}
}

func (s *Server) renderUIFragment(w http.ResponseWriter, fragment string, data any) {
	tmpl, ok := s.uiFragments[fragment]
	if !ok {
		s.logger.Error("renderUIFragment: unknown fragment", "fragment", fragment)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, fragment, data); err != nil {
		s.logger.Error("renderUIFragment: execute failed", "fragment", fragment, "error", err)
	}
}

// isAdminUser reports whether user holds the admin permission, used to decide
// which nav links and actions to render. Handlers still enforce access via
// requirePermissions; this only controls UI visibility.
func isAdminUser(user *User) bool {
	return user != nil && hasPermission(user.Roles, PermAdmin)
}
