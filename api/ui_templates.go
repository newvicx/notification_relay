package api

import (
	"net/http"
	"strings"
)

type uiTemplateRow struct {
	TemplateName string
	Description  string
	RequiredVars string
}

type uiTemplatesListData struct {
	Templates []uiTemplateRow
}

func (s *Server) handleUIListTemplates(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	templates, err := s.listTemplatesCore(r.Context())
	if err != nil {
		writeCoreError(w, err)
		return
	}

	rows := make([]uiTemplateRow, len(templates))
	for i, t := range templates {
		tr := toTemplateResponse(t)
		rows[i] = uiTemplateRow{
			TemplateName: tr.TemplateName,
			Description:  tr.Description,
			RequiredVars: strings.Join(tr.RequiredVars, ", "),
		}
	}

	s.renderUIPage(w, "templates_list.html", uiPageData{
		Title: "Templates", User: user, IsAdmin: isAdminUser(user),
		Data: uiTemplatesListData{Templates: rows},
	})
}

// uiTemplateFormData backs both the create and edit forms.
type uiTemplateFormData struct {
	Editing      bool
	TemplateName string
	Subject      string
	Body         string
	RequiredVars string
	Description  string
}

func parseRequiredVars(s string) []string {
	var vars []string
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			vars = append(vars, v)
		}
	}
	return vars
}

func (s *Server) handleUINewTemplateForm(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	s.renderUIPage(w, "template_form.html", uiPageData{
		Title: "New Template", User: user, IsAdmin: isAdminUser(user),
		Data: uiTemplateFormData{Editing: false},
	})
}

func (s *Server) handleUICreateTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	form := uiTemplateFormData{
		Editing:      false,
		TemplateName: r.FormValue("template_name"),
		Subject:      r.FormValue("subject"),
		Body:         r.FormValue("body"),
		RequiredVars: r.FormValue("required_vars"),
		Description:  r.FormValue("description"),
	}

	req := templateRequest{
		TemplateName: form.TemplateName,
		Subject:      form.Subject,
		Body:         form.Body,
		RequiredVars: parseRequiredVars(form.RequiredVars),
		Description:  form.Description,
	}

	if _, err := s.createTemplateCore(r, req); err != nil {
		s.renderUIPage(w, "template_form.html", uiPageData{
			Title: "New Template", User: user, IsAdmin: isAdminUser(user),
			Flash: err.Error(), FlashClass: "error",
			Data: form,
		})
		return
	}

	http.Redirect(w, r, "/ui/templates", http.StatusSeeOther)
}

func (s *Server) handleUIEditTemplateForm(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	name := r.PathValue("template_name")

	tmpl, err := s.getTemplateCore(r.Context(), name)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	tr := toTemplateResponse(tmpl)

	s.renderUIPage(w, "template_form.html", uiPageData{
		Title: "Edit Template", User: user, IsAdmin: isAdminUser(user),
		Data: uiTemplateFormData{
			Editing:      true,
			TemplateName: tr.TemplateName,
			Subject:      tr.Subject,
			Body:         tr.Body,
			RequiredVars: strings.Join(tr.RequiredVars, ", "),
			Description:  tr.Description,
		},
	})
}

func (s *Server) handleUIUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	name := r.PathValue("template_name")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	form := uiTemplateFormData{
		Editing:      true,
		TemplateName: name,
		Subject:      r.FormValue("subject"),
		Body:         r.FormValue("body"),
		RequiredVars: r.FormValue("required_vars"),
		Description:  r.FormValue("description"),
	}

	req := templateRequest{
		Subject:      form.Subject,
		Body:         form.Body,
		RequiredVars: parseRequiredVars(form.RequiredVars),
		Description:  form.Description,
	}

	if _, err := s.updateTemplateCore(r, name, req); err != nil {
		s.renderUIPage(w, "template_form.html", uiPageData{
			Title: "Edit Template", User: user, IsAdmin: isAdminUser(user),
			Flash: err.Error(), FlashClass: "error",
			Data: form,
		})
		return
	}

	http.Redirect(w, r, "/ui/templates", http.StatusSeeOther)
}

func (s *Server) handleUIDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")
	if err := s.deleteTemplateCore(r, name); err != nil {
		writeCoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
