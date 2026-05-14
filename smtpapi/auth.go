package smtpapi

// Permission represents a named capability enforced at the SMTP layer.
type Permission string

const (
	permPublish Permission = "publish"
)

var rolePermissions = map[string][]Permission{
	"admin":     {permPublish},
	"publisher": {permPublish},
}

// resolveRoles returns the role names granted by the user's LDAP group memberships.
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
