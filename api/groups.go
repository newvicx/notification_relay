package api

import (
	"encoding/json"
	"net/http"

	"notification_relay/db"
)

type groupMemberResponse struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Mobile      string `json:"mobile"`
	Work        string `json:"work"`
	SyncedAt    string `json:"synced_at"`
}

func toGroupMemberResponse(m db.GroupMember) groupMemberResponse {
	return groupMemberResponse{
		Username:    m.Username,
		DisplayName: m.DisplayName.String,
		Email:       m.Email.String,
		Mobile:      m.Mobile.String,
		Work:        m.Work.String,
		SyncedAt:    m.SyncedAt,
	}
}

// handleListGroups returns all distinct group names that have been synced from LDAP.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.q.ListDistinctGroupNames(r.Context())
	if err != nil {
		s.logger.Error("list groups failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if groups == nil {
		groups = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

// handleListGroupMembers returns all members of the named LDAP group.
// Returns 404 if the group name is unknown (no members found).
func (s *Server) handleListGroupMembers(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group_name")
	members, err := s.q.ListGroupMembers(r.Context(), groupName)
	if err != nil {
		s.logger.Error("list group members failed", "group", groupName, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(members) == 0 {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}
	resp := make([]groupMemberResponse, len(members))
	for i, m := range members {
		resp[i] = toGroupMemberResponse(m)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
