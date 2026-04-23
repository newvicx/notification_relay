package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"notification_relay/db"
	"notification_relay/notify"
)

type templateRequest struct {
	TemplateName string   `json:"template_name"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	RequiredVars []string `json:"required_vars"`
	Description  string   `json:"description"`
}

// validateTemplateRequest checks required fields and validates the template
// syntax and required variable references. Returns an error message suitable
// for a 422 response, or an empty string if valid.
func validateTemplateRequest(req templateRequest) string {
	if req.TemplateName == "" {
		return "template_name is required"
	}
	if req.Subject == "" {
		return "subject is required"
	}
	if req.Body == "" {
		return "body is required"
	}
	if err := notify.ValidateTemplate(req.Subject, req.Body, req.RequiredVars); err != nil {
		return err.Error()
	}
	return ""
}

// handleCreateTemplate registers a new email template.
// Returns 409 if a template with the same name already exists.
func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if msg := validateTemplateRequest(req); msg != "" {
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}

	ctx := r.Context()

	// Check for duplicate name.
	if _, err := s.q.GetEmailTemplateByName(ctx, req.TemplateName); err == nil {
		http.Error(w, "template already exists", http.StatusConflict)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create template: check existing failed", "name", req.TemplateName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	requiredVarsJSON, err := json.Marshal(req.RequiredVars)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	tmpl, err := s.q.InsertEmailTemplate(ctx, db.InsertEmailTemplateParams{
		TemplateName: req.TemplateName,
		Subject:      req.Subject,
		Body:         req.Body,
		RequiredVars: string(requiredVarsJSON),
		Description:  nullString(req.Description),
	})
	if err != nil {
		s.logger.Error("create template: insert failed", "name", req.TemplateName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "create_template", "email_templates", "", marshalAuditJSON(tmpl))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// handleListTemplates returns all registered email templates.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.q.ListEmailTemplates(r.Context())
	if err != nil {
		s.logger.Error("list templates failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := make([]templateResponse, len(templates))
	for i, t := range templates {
		resp[i] = toTemplateResponse(t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleGetTemplate returns a single template by name.
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")

	tmpl, err := s.q.GetEmailTemplateByName(r.Context(), name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "template not found", http.StatusNotFound)
			return
		}
		s.logger.Error("get template failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// handleUpdateTemplate replaces an existing template's subject, body, required
// vars, and description. The template is re-validated before updating.
func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")

	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// template_name in body is ignored for updates; use path value.
	req.TemplateName = name
	if msg := validateTemplateRequest(req); msg != "" {
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}

	ctx := r.Context()

	// Fetch the existing record for old-values snapshot and existence check.
	oldTmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "template not found", http.StatusNotFound)
			return
		}
		s.logger.Error("update template: get failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	requiredVarsJSON, err := json.Marshal(req.RequiredVars)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.q.UpdateEmailTemplate(ctx, db.UpdateEmailTemplateParams{
		TemplateName: name,
		Subject:      req.Subject,
		Body:         req.Body,
		RequiredVars: string(requiredVarsJSON),
		Description:  nullString(req.Description),
	}); err != nil {
		s.logger.Error("update template: update failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Re-fetch for new-values snapshot and response.
	tmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		s.logger.Error("update template: re-fetch failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "update_template", "email_templates", marshalAuditJSON(oldTmpl), marshalAuditJSON(tmpl))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// handleDeleteTemplate removes a template by name.
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")
	ctx := r.Context()

	tmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "template not found", http.StatusNotFound)
			return
		}
		s.logger.Error("delete template: get failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.q.DeleteEmailTemplate(ctx, name); err != nil {
		s.logger.Error("delete template: delete failed", "name", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "delete_template", "email_templates", marshalAuditJSON(tmpl), "")

	w.WriteHeader(http.StatusNoContent)
}

// templateResponse is the JSON representation of an email template.
// required_vars is decoded from its JSON-array storage format.
type templateResponse struct {
	ID           int64    `json:"id"`
	TemplateName string   `json:"template_name"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	RequiredVars []string `json:"required_vars"`
	Description  string   `json:"description"`
}

func toTemplateResponse(t db.EmailTemplate) templateResponse {
	var vars []string
	json.Unmarshal([]byte(t.RequiredVars), &vars) //nolint:errcheck — stored as valid JSON
	if vars == nil {
		vars = []string{}
	}
	return templateResponse{
		ID:           t.ID,
		TemplateName: t.TemplateName,
		Subject:      t.Subject,
		Body:         t.Body,
		RequiredVars: vars,
		Description:  t.Description.String,
	}
}
