package api

import "context"

// Permission represents a named capability enforced at the HTTP layer.
type Permission string

const (
	PermAdmin   Permission = "admin"
	PermPublish Permission = "publish"
	PermRead    Permission = "read"
)

// rolePermissions defines which permissions each role grants.
// Roles and their permissions are fixed in code; only the group→role
// assignment is configurable via ldap.roles in the YAML config.
var rolePermissions = map[string][]Permission{
	"admin":     {PermAdmin, PermPublish, PermRead},
	"publisher": {PermPublish, PermRead},
	"reader":    {PermRead},
}

// User is the authenticated and authorized request principal. It is stored in
// the request context by requirePermission and should be retrieved by handlers
// via UserFromContext for use in log fields and audit log entries.
type User struct {
	Username string
	DN       string // LDAP distinguished name
	Roles    []string
}

type ctxKey struct{}

// UserFromContext retrieves the authenticated User from ctx.
// Returns (nil, false) if the request did not pass through requirePermission.
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok
}

// resolveRoles returns the role names for which the user is a member of at
// least one of the configured groups. roleConfig maps role names to group CNs.
func resolveRoles(groups []string, roleConfig map[string][]string) []string {
	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupSet[g] = struct{}{}
	}
	var roles []string
	for role, groupList := range roleConfig {
		for _, g := range groupList {
			if _, ok := groupSet[g]; ok {
				roles = append(roles, role)
				break
			}
		}
	}
	return roles
}

// hasPermission returns true if any of the given roles grants perm.
func hasPermission(roles []string, perm Permission) bool {
	for _, role := range roles {
		for _, p := range rolePermissions[role] {
			if p == perm {
				return true
			}
		}
	}
	return false
}

