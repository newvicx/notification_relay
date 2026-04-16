package ldap

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// Syncer periodically syncs LDAP group membership into the group_members table.
// It performs a full delete + reinsert for each configured group on every sync
// cycle. group_members is a reference snapshot with no FK dependents, so this
// is safe and simpler than reconciliation.
type Syncer struct {
	cfg    config.LDAPConfig
	client Client
	q      *db.Queries // for reading sync_groups
	writer *sql.DB
	logger *slog.Logger
}

// NewSyncer constructs a Syncer. writer must be the single-connection writer DB.
func NewSyncer(cfg config.LDAPConfig, client Client, writer *sql.DB, logger *slog.Logger) *Syncer {
	return &Syncer{
		cfg:    cfg,
		client: client,
		q:      db.New(writer),
		writer: writer,
		logger: logger,
	}
}

// Run starts the sync loop. It syncs immediately on startup, then on each
// ticker interval. It returns when ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SyncInterval)
	defer ticker.Stop()

	s.syncOnce(ctx)

	for {
		select {
		case <-ticker.C:
			s.syncOnce(ctx)
		case <-ctx.Done():
			s.logger.Info("ldap syncer shutting down")
			return
		}
	}
}

func (s *Syncer) syncOnce(ctx context.Context) {
	syncGroups, err := s.q.ListSyncGroups(ctx)
	if err != nil {
		s.logger.Error("ldap sync failed: list sync groups", "error", err)
		return
	}

	s.cleanupGroups(ctx, syncGroups)

	if len(syncGroups) == 0 {
		s.logger.Info("ldap sync skipped: no sync groups configured")
		return
	}

	groupNames := make([]string, len(syncGroups))
	for i, g := range syncGroups {
		groupNames[i] = g.GroupName
	}

	start := time.Now()
	s.logger.Info("ldap sync started", "groups", groupNames)

	if err := s.client.Connect(ctx); err != nil {
		s.logger.Error("ldap sync failed: connect", "error", err)
		return
	}
	defer func() {
		if err := s.client.Close(); err != nil {
			s.logger.Warn("ldap close error", "error", err)
		}
	}()

	var totalMembers int
	var groupErrors int

	for _, g := range syncGroups {
		count, err := s.syncGroup(ctx, g.GroupName)
		if err != nil {
			s.logger.Error("ldap sync failed for group",
				"group", g.GroupName,
				"error", err,
			)
			groupErrors++
			continue
		}
		totalMembers += count
		s.logger.Info("ldap group synced",
			"group", g.GroupName,
			"member_count", count,
		)
	}

	s.logger.Info("ldap sync completed",
		"groups_synced", len(syncGroups)-groupErrors,
		"groups_failed", groupErrors,
		"total_members", totalMembers,
		"duration", time.Since(start).String(),
	)
}

// syncGroup replaces all rows for groupName with the current LDAP membership.
// It runs inside a transaction so readers always see a consistent snapshot.
func (s *Syncer) syncGroup(ctx context.Context, groupName string) (int, error) {
	members, err := s.client.SearchGroupMembers(ctx, groupName, s.cfg.PageSize)
	if err != nil {
		return 0, fmt.Errorf("search members: %w", err)
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	qtx := db.New(tx)

	if err = qtx.DeleteGroupMembers(ctx, groupName); err != nil {
		return 0, fmt.Errorf("delete existing members: %w", err)
	}

	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, m := range members {
		if m.Username == "" {
			s.logger.Warn("skipping member with empty username", "group", groupName, "display_name", m.DisplayName)
			continue
		}
		if err = qtx.InsertGroupMember(ctx, db.InsertGroupMemberParams{
			GroupName:   groupName,
			Username:    m.Username,
			DisplayName: toNullableString(m.DisplayName),
			Email:       toNullableString(m.Email),
			Mobile:      toNullableString(m.Mobile),
			Work:        toNullableString(m.Work),
			SyncedAt:    syncedAt,
		}); err != nil {
			return 0, fmt.Errorf("insert member %q: %w", m.Username, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return len(members), nil
}

// cleanupGroups removes group_members rows for any group that is no longer
// present in sync_groups. It is called on every sync cycle so that groups
// removed from configuration don't leave stale membership data behind.
func (s *Syncer) cleanupGroups(ctx context.Context, activeSyncGroups []db.SyncGroup) {
	activeSet := make(map[string]struct{}, len(activeSyncGroups))
	for _, g := range activeSyncGroups {
		activeSet[g.GroupName] = struct{}{}
	}

	groupNames, err := s.q.ListDistinctGroupNames(ctx)
	if err != nil {
		s.logger.Error("ldap cleanup: list distinct group names", "error", err)
		return
	}

	for _, name := range groupNames {
		if _, ok := activeSet[name]; ok {
			continue
		}
		if err := s.q.DeleteGroupMembers(ctx, name); err != nil {
			s.logger.Error("ldap cleanup: delete orphaned group", "group", name, "error", err)
			continue
		}
		s.logger.Info("ldap cleanup: removed orphaned group", "group", name)
	}
}

func toNullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
