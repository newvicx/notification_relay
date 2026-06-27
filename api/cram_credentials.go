package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"notification_relay/db"
	"notification_relay/smtpapi"
)

var knownRoles = map[string]bool{"admin": true, "publisher": true, "reader": true}

type cramCredentialResponse struct {
	Username  string   `json:"username"`
	Roles     []string `json:"roles"`
	Enabled   bool     `json:"enabled"`
	CreatedAt string   `json:"created_at"`
}

func toCRAMCredentialResponse(c db.SmtpCramCredential) cramCredentialResponse {
	var roles []string
	_ = json.Unmarshal([]byte(c.Roles), &roles)
	return cramCredentialResponse{
		Username:  c.Username,
		Roles:     roles,
		Enabled:   c.Enabled != 0,
		CreatedAt: c.CreatedAt,
	}
}

// handleListCRAMCredentials lists SMTP CRAM-MD5 credentials. The secret is
// never returned — it can only be seen once, at creation time.
func (s *Server) handleListCRAMCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := s.q.ListCRAMCredentials(r.Context())
	if err != nil {
		s.logger.Error("list cram credentials failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := make([]cramCredentialResponse, len(creds))
	for i, c := range creds {
		resp[i] = toCRAMCredentialResponse(c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCreateCRAMCredential creates a new SMTP CRAM-MD5 credential with a
// randomly generated secret, encrypted at rest under the server's
// cram_md5_secret_key. The plaintext secret is returned exactly once, in this
// response — it cannot be retrieved again.
func (s *Server) handleCreateCRAMCredential(w http.ResponseWriter, r *http.Request) {
	if s.cramKey == nil {
		http.Error(w, "smtp_server.cram_md5_enabled is not configured", http.StatusUnprocessableEntity)
		return
	}

	var req struct {
		Username string   `json:"username"`
		Roles    []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		http.Error(w, "username is required", http.StatusUnprocessableEntity)
		return
	}
	if len(req.Roles) == 0 {
		http.Error(w, "roles is required", http.StatusUnprocessableEntity)
		return
	}
	for _, role := range req.Roles {
		if !knownRoles[role] {
			http.Error(w, "unknown role "+role+" (must be one of: admin, publisher, reader)", http.StatusUnprocessableEntity)
			return
		}
	}

	ctx := r.Context()

	if _, err := s.q.GetCRAMCredential(ctx, req.Username); err == nil {
		http.Error(w, "cram credential already exists", http.StatusConflict)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create cram credential: check existing failed", "username", req.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		s.logger.Error("create cram credential: generate secret failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	secret := base64.StdEncoding.EncodeToString(secretBytes)

	nonceB64, cipherB64, err := smtpapi.EncryptSecret(s.cramKey, secret)
	if err != nil {
		s.logger.Error("create cram credential: encrypt secret failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rolesJSON, err := json.Marshal(req.Roles)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.q.InsertCRAMCredential(ctx, db.InsertCRAMCredentialParams{
		Username:     req.Username,
		SecretNonce:  nonceB64,
		SecretCipher: cipherB64,
		Roles:        string(rolesJSON),
		Enabled:      1,
		CreatedAt:    now,
	}); err != nil {
		s.logger.Error("create cram credential: insert failed", "username", req.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "create_cram_credential", "smtp_cram_credentials", "",
		marshalAuditJSON(cramCredentialResponse{Username: req.Username, Roles: req.Roles, Enabled: true, CreatedAt: now}))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		Username  string   `json:"username"`
		Roles     []string `json:"roles"`
		CreatedAt string   `json:"created_at"`
		Secret    string   `json:"secret"`
	}{
		Username:  req.Username,
		Roles:     req.Roles,
		CreatedAt: now,
		Secret:    secret,
	})
}

// handleDeleteCRAMCredential removes a CRAM-MD5 credential.
func (s *Server) handleDeleteCRAMCredential(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	ctx := r.Context()

	cred, err := s.q.GetCRAMCredential(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "cram credential not found", http.StatusNotFound)
			return
		}
		s.logger.Error("delete cram credential: get failed", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.q.DeleteCRAMCredential(ctx, username); err != nil {
		s.logger.Error("delete cram credential: delete failed", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "delete_cram_credential", "smtp_cram_credentials", marshalAuditJSON(toCRAMCredentialResponse(cred)), "")

	w.WriteHeader(http.StatusNoContent)
}
