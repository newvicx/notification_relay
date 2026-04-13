package api

import (
	"context"
	"testing"
)

func TestResolveRoles(t *testing.T) {
	roleConfig := map[string][]string{
		"admin":     {"grp-admins"},
		"publisher": {"grp-oncall", "grp-ops"},
		"reader":    {"grp-monitoring"},
	}

	tests := []struct {
		name   string
		groups []string
		want   []string
	}{
		{
			name:   "single group maps to single role",
			groups: []string{"grp-admins"},
			want:   []string{"admin"},
		},
		{
			name:   "group not in config produces no role",
			groups: []string{"grp-unknown"},
			want:   nil,
		},
		{
			name:   "member of two role groups gets both roles",
			groups: []string{"grp-oncall", "grp-monitoring"},
			want:   []string{"publisher", "reader"},
		},
		{
			name:   "empty groups produces no roles",
			groups: []string{},
			want:   nil,
		},
		{
			name:   "second group for same role still yields one role",
			groups: []string{"grp-oncall", "grp-ops"},
			want:   []string{"publisher"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRoles(tc.groups, roleConfig)
			if len(got) != len(tc.want) {
				t.Fatalf("resolveRoles(%v) = %v, want %v", tc.groups, got, tc.want)
			}
			wantSet := make(map[string]bool, len(tc.want))
			for _, r := range tc.want {
				wantSet[r] = true
			}
			for _, r := range got {
				if !wantSet[r] {
					t.Errorf("unexpected role %q in result %v", r, got)
				}
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		roles []string
		perm  Permission
		want  bool
	}{
		{[]string{"admin"}, PermAdmin, true},
		{[]string{"admin"}, PermPublish, true},
		{[]string{"admin"}, PermRead, true},
		{[]string{"publisher"}, PermAdmin, false},
		{[]string{"publisher"}, PermPublish, true},
		{[]string{"publisher"}, PermRead, true},
		{[]string{"reader"}, PermAdmin, false},
		{[]string{"reader"}, PermPublish, false},
		{[]string{"reader"}, PermRead, true},
		{[]string{}, PermRead, false},
		{[]string{"unknown"}, PermRead, false},
	}

	for _, tc := range tests {
		got := hasPermission(tc.roles, tc.perm)
		if got != tc.want {
			t.Errorf("hasPermission(%v, %q) = %v, want %v", tc.roles, tc.perm, got, tc.want)
		}
	}
}

func TestUserFromContext(t *testing.T) {
	t.Run("absent returns false", func(t *testing.T) {
		_, ok := UserFromContext(context.Background())
		if ok {
			t.Fatal("expected (nil, false) for empty context")
		}
	})

	t.Run("present returns user", func(t *testing.T) {
		u := &User{Username: "alice", DN: "CN=alice,DC=example,DC=com", Roles: []string{"publisher"}}
		ctx := context.WithValue(context.Background(), ctxKey{}, u)
		got, ok := UserFromContext(ctx)
		if !ok {
			t.Fatal("expected user to be found in context")
		}
		if got.Username != u.Username || got.DN != u.DN {
			t.Errorf("got %+v, want %+v", got, u)
		}
	})
}
