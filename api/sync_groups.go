package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"notification_relay/db"
)

type syncGroupResponse struct {
	ID        int64  `json:"id"`
	GroupName string `json:"group_name"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

func toSyncGroupResponse(g db.SyncGroup) syncGroupResponse {
	return syncGroupResponse{
		ID:        g.ID,
		GroupName: g.GroupName,
		CreatedAt: g.CreatedAt,
		CreatedBy: g.CreatedBy,
	}
}

// handleListSyncGroups returns all configured sync groups.
func (s *Server) handleListSyncGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.q.ListSyncGroups(r.Context())
	if err != nil {
		s.logger.Error("list sync groups failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := make([]syncGroupResponse, len(groups))
	for i, g := range groups {
		resp[i] = toSyncGroupResponse(g)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCreateSyncGroup adds a new LDAP group to the sync list.
// Returns 409 if the group is already configured.
func (s *Server) handleCreateSyncGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.GroupName == "" {
		http.Error(w, "group_name is required", http.StatusUnprocessableEntity)
		return
	}

	ctx := r.Context()
	user, _ := UserFromContext(ctx)

	if _, err := s.q.GetSyncGroup(ctx, req.GroupName); err == nil {
		http.Error(w, "sync group already exists", http.StatusConflict)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create sync group: check existing failed", "name", req.GroupName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	g, err := s.q.InsertSyncGroup(ctx, db.InsertSyncGroupParams{
		GroupName: req.GroupName,
		CreatedBy: user.Username,
	})
	if err != nil {
		s.logger.Error("create sync group: insert failed", "name", req.GroupName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "create_sync_group", "sync_groups", "", marshalAuditJSON(g))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toSyncGroupResponse(g))
}

// handleDeleteSyncGroup removes a group from the sync list.
// Returns 404 if the group is not configured.
func (s *Server) handleDeleteSyncGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group_name")
	ctx := r.Context()

	g, err := s.q.GetSyncGroup(ctx, groupName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "sync group not found", http.StatusNotFound)
			return
		}
		s.logger.Error("delete sync group: get failed", "name", groupName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.q.DeleteSyncGroup(ctx, groupName); err != nil {
		s.logger.Error("delete sync group: delete failed", "name", groupName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "delete_sync_group", "sync_groups", marshalAuditJSON(g), "")

	w.WriteHeader(http.StatusNoContent)
}
