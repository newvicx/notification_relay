package api

import "net/http"

type uiSyncGroupRow struct {
	GroupName string
	CreatedBy string
	CreatedAt string
}

type uiSyncGroupsData struct {
	Groups []uiSyncGroupRow
}

func (s *Server) renderSyncGroupsPage(w http.ResponseWriter, r *http.Request, flash, flashClass string) {
	user, _ := UserFromContext(r.Context())

	groups, err := s.listSyncGroupsCore(r.Context())
	if err != nil {
		writeCoreError(w, err)
		return
	}

	rows := make([]uiSyncGroupRow, len(groups))
	for i, g := range groups {
		rows[i] = uiSyncGroupRow{GroupName: g.GroupName, CreatedBy: g.CreatedBy, CreatedAt: g.CreatedAt}
	}

	s.renderUIPage(w, "sync_groups.html", uiPageData{
		Title: "Sync Groups", User: user, IsAdmin: isAdminUser(user),
		Flash: flash, FlashClass: flashClass,
		Data: uiSyncGroupsData{Groups: rows},
	})
}

func (s *Server) handleUIListSyncGroups(w http.ResponseWriter, r *http.Request) {
	s.renderSyncGroupsPage(w, r, "", "")
}

func (s *Server) handleUICreateSyncGroup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderSyncGroupsPage(w, r, "Invalid form submission.", "error")
		return
	}
	groupName := r.FormValue("group_name")

	if _, err := s.createSyncGroupCore(r, groupName); err != nil {
		s.renderSyncGroupsPage(w, r, err.Error(), "error")
		return
	}

	http.Redirect(w, r, "/ui/groups/sync", http.StatusSeeOther)
}

func (s *Server) handleUIDeleteSyncGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group_name")
	if err := s.deleteSyncGroupCore(r, groupName); err != nil {
		writeCoreError(w, err)
		return
	}
	// htmx swap target is the deleted row; empty body removes it via outerHTML swap.
	w.WriteHeader(http.StatusOK)
}
