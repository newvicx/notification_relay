package api

import (
	"context"
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

// createTemplateCore registers a new email template.
// Returns a 409 coreError if a template with the same name already exists.
// Shared by the JSON API and the UI template create page.
func (s *Server) createTemplateCore(r *http.Request, req templateRequest) (db.EmailTemplate, error) {
	if msg := validateTemplateRequest(req); msg != "" {
		return db.EmailTemplate{}, newCoreError(http.StatusUnprocessableEntity, msg)
	}

	ctx := r.Context()

	// Check for duplicate name.
	if _, err := s.q.GetEmailTemplateByName(ctx, req.TemplateName); err == nil {
		return db.EmailTemplate{}, newCoreError(http.StatusConflict, "template already exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create template: check existing failed", "name", req.TemplateName, "error", err)
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	requiredVarsJSON, err := json.Marshal(req.RequiredVars)
	if err != nil {
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
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
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	s.auditLogAction(r, "create_template", "email_templates", "", marshalAuditJSON(tmpl))

	return tmpl, nil
}

// handleCreateTemplate is the JSON API wrapper around createTemplateCore.
func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	tmpl, err := s.createTemplateCore(r, req)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// listTemplatesCore returns all registered email templates.
// Shared by the JSON API and the UI templates list page.
func (s *Server) listTemplatesCore(ctx context.Context) ([]db.EmailTemplate, error) {
	templates, err := s.q.ListEmailTemplates(ctx)
	if err != nil {
		s.logger.Error("list templates failed", "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}
	return templates, nil
}

// handleListTemplates is the JSON API wrapper around listTemplatesCore.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.listTemplatesCore(r.Context())
	if err != nil {
		writeCoreError(w, err)
		return
	}

	resp := make([]templateResponse, len(templates))
	for i, t := range templates {
		resp[i] = toTemplateResponse(t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// getTemplateCore returns a single template by name.
// Shared by the JSON API and the UI template edit page.
func (s *Server) getTemplateCore(ctx context.Context, name string) (db.EmailTemplate, error) {
	tmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.EmailTemplate{}, newCoreError(http.StatusNotFound, "template not found")
		}
		s.logger.Error("get template failed", "name", name, "error", err)
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}
	return tmpl, nil
}

// handleGetTemplate is the JSON API wrapper around getTemplateCore.
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")

	tmpl, err := s.getTemplateCore(r.Context(), name)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// updateTemplateCore replaces an existing template's subject, body, required
// vars, and description. The template is re-validated before updating.
// Shared by the JSON API and the UI template edit page.
func (s *Server) updateTemplateCore(r *http.Request, name string, req templateRequest) (db.EmailTemplate, error) {
	// template_name in the request is ignored for updates; the path/caller-supplied name wins.
	req.TemplateName = name
	if msg := validateTemplateRequest(req); msg != "" {
		return db.EmailTemplate{}, newCoreError(http.StatusUnprocessableEntity, msg)
	}

	ctx := r.Context()

	// Fetch the existing record for old-values snapshot and existence check.
	oldTmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.EmailTemplate{}, newCoreError(http.StatusNotFound, "template not found")
		}
		s.logger.Error("update template: get failed", "name", name, "error", err)
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	requiredVarsJSON, err := json.Marshal(req.RequiredVars)
	if err != nil {
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	if err := s.q.UpdateEmailTemplate(ctx, db.UpdateEmailTemplateParams{
		TemplateName: name,
		Subject:      req.Subject,
		Body:         req.Body,
		RequiredVars: string(requiredVarsJSON),
		Description:  nullString(req.Description),
	}); err != nil {
		s.logger.Error("update template: update failed", "name", name, "error", err)
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	// Re-fetch for new-values snapshot and response.
	tmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		s.logger.Error("update template: re-fetch failed", "name", name, "error", err)
		return db.EmailTemplate{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	s.auditLogAction(r, "update_template", "email_templates", marshalAuditJSON(oldTmpl), marshalAuditJSON(tmpl))

	return tmpl, nil
}

// handleUpdateTemplate is the JSON API wrapper around updateTemplateCore.
func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")

	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	tmpl, err := s.updateTemplateCore(r, name, req)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTemplateResponse(tmpl))
}

// deleteTemplateCore removes a template by name.
// Shared by the JSON API and the UI templates list page.
func (s *Server) deleteTemplateCore(r *http.Request, name string) error {
	ctx := r.Context()

	tmpl, err := s.q.GetEmailTemplateByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return newCoreError(http.StatusNotFound, "template not found")
		}
		s.logger.Error("delete template: get failed", "name", name, "error", err)
		return newCoreError(http.StatusInternalServerError, "internal server error")
	}

	if err := s.q.DeleteEmailTemplate(ctx, name); err != nil {
		s.logger.Error("delete template: delete failed", "name", name, "error", err)
		return newCoreError(http.StatusInternalServerError, "internal server error")
	}

	s.auditLogAction(r, "delete_template", "email_templates", marshalAuditJSON(tmpl), "")

	return nil
}

// handleDeleteTemplate is the JSON API wrapper around deleteTemplateCore.
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("template_name")

	if err := s.deleteTemplateCore(r, name); err != nil {
		writeCoreError(w, err)
		return
	}

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
