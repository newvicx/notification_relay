package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"notification_relay/db"
)

type auditLogResponse struct {
	ID            int64  `json:"id"`
	Timestamp     string `json:"timestamp"`
	Username      string `json:"username"`
	IPAddress     string `json:"ip_address"`
	Action        string `json:"action"`
	ImpactedTable string `json:"impacted_table"`
	OldValues     string `json:"old_values"`
	NewValues     string `json:"new_values"`
}

func toAuditLogResponse(a db.AuditLog) auditLogResponse {
	return auditLogResponse{
		ID:            a.ID,
		Timestamp:     a.Timestamp,
		Username:      a.Username,
		IPAddress:     a.IpAddress.String,
		Action:        a.Action,
		ImpactedTable: a.ImpactedTable,
		OldValues:     a.OldValues.String,
		NewValues:     a.NewValues.String,
	}
}

// handleListAuditLog returns a paginated, optionally filtered audit log.
// Query params:
//   - username: filter to a specific user (optional)
//   - from:     RFC 3339 lower bound on timestamp, inclusive (optional)
//   - to:       RFC 3339 upper bound on timestamp, inclusive (optional)
//   - limit:    default 50, max 200
//   - offset:   default 0
func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	offset := int64(0)

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 200 {
			http.Error(w, "limit must be an integer between 1 and 200", http.StatusBadRequest)
			return
		}
		limit = n
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
			return
		}
		offset = n
	}

	// Optional filters; empty string means "no filter" in the SQL query.
	username := r.URL.Query().Get("username")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	entries, err := s.q.ListAuditLogFiltered(r.Context(), db.ListAuditLogFilteredParams{
		Username: username,
		FromTime: from,
		ToTime:   to,
		Offset:   offset,
		Limit:    limit,
	})
	if err != nil {
		s.logger.Error("list audit log failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := make([]auditLogResponse, len(entries))
	for i, a := range entries {
		resp[i] = toAuditLogResponse(a)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Entries []auditLogResponse `json:"entries"`
		Limit   int64              `json:"limit"`
		Offset  int64              `json:"offset"`
	}{Entries: resp, Limit: limit, Offset: offset})
}
