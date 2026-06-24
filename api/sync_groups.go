package api

import (
	"context"
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

// listSyncGroupsCore returns all configured sync groups.
// Shared by the JSON API and the UI sync groups page.
func (s *Server) listSyncGroupsCore(ctx context.Context) ([]db.SyncGroup, error) {
	groups, err := s.q.ListSyncGroups(ctx)
	if err != nil {
		s.logger.Error("list sync groups failed", "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}
	return groups, nil
}

// handleListSyncGroups is the JSON API wrapper around listSyncGroupsCore.
func (s *Server) handleListSyncGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.listSyncGroupsCore(r.Context())
	if err != nil {
		writeCoreError(w, err)
		return
	}
	resp := make([]syncGroupResponse, len(groups))
	for i, g := range groups {
		resp[i] = toSyncGroupResponse(g)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// createSyncGroupCore adds a new LDAP group to the sync list.
// Returns a 409 coreError if the group is already configured, or 422 if the
// group does not exist in LDAP. Shared by the JSON API and the UI sync groups page.
func (s *Server) createSyncGroupCore(r *http.Request, groupName string) (db.SyncGroup, error) {
	if groupName == "" {
		return db.SyncGroup{}, newCoreError(http.StatusUnprocessableEntity, "group_name is required")
	}

	ctx := r.Context()
	user, _ := UserFromContext(ctx)

	if _, err := s.q.GetSyncGroup(ctx, groupName); err == nil {
		return db.SyncGroup{}, newCoreError(http.StatusConflict, "sync group already exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create sync group: check existing failed", "name", groupName, "error", err)
		return db.SyncGroup{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	if err := s.groupVerifier.VerifyGroup(ctx, groupName); err != nil {
		s.logger.Warn("create sync group: ldap verification failed", "name", groupName, "error", err)
		return db.SyncGroup{}, newCoreError(http.StatusUnprocessableEntity, "group not found in LDAP: "+err.Error())
	}

	g, err := s.q.InsertSyncGroup(ctx, db.InsertSyncGroupParams{
		GroupName: groupName,
		CreatedBy: user.Username,
	})
	if err != nil {
		s.logger.Error("create sync group: insert failed", "name", groupName, "error", err)
		return db.SyncGroup{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	s.auditLogAction(r, "create_sync_group", "sync_groups", "", marshalAuditJSON(g))

	return g, nil
}

// handleCreateSyncGroup is the JSON API wrapper around createSyncGroupCore.
func (s *Server) handleCreateSyncGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	g, err := s.createSyncGroupCore(r, req.GroupName)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toSyncGroupResponse(g))
}

// deleteSyncGroupCore removes a group from the sync list.
// Returns a 404 coreError if the group is not configured.
// Shared by the JSON API and the UI sync groups page.
func (s *Server) deleteSyncGroupCore(r *http.Request, groupName string) error {
	ctx := r.Context()

	g, err := s.q.GetSyncGroup(ctx, groupName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return newCoreError(http.StatusNotFound, "sync group not found")
		}
		s.logger.Error("delete sync group: get failed", "name", groupName, "error", err)
		return newCoreError(http.StatusInternalServerError, "internal server error")
	}

	if err := s.q.DeleteSyncGroup(ctx, groupName); err != nil {
		s.logger.Error("delete sync group: delete failed", "name", groupName, "error", err)
		return newCoreError(http.StatusInternalServerError, "internal server error")
	}

	s.auditLogAction(r, "delete_sync_group", "sync_groups", marshalAuditJSON(g), "")

	return nil
}

// handleDeleteSyncGroup is the JSON API wrapper around deleteSyncGroupCore.
func (s *Server) handleDeleteSyncGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group_name")

	if err := s.deleteSyncGroupCore(r, groupName); err != nil {
		writeCoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
