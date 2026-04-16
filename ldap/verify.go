package ldap

import (
	"context"
	"fmt"

	goldap "github.com/go-ldap/ldap/v3"
)

// GroupVerifier checks whether a named group exists in LDAP.
// Each call opens its own connection so it is safe to call concurrently
// from multiple HTTP request goroutines without interfering with the syncer.
type GroupVerifier interface {
	VerifyGroup(ctx context.Context, groupName string) error
}

type ldapGroupVerifier struct {
	primaryURL    string
	backupURL     string
	bindDN        string
	bindPassword  string
	groupBaseDN   string
	groupFilter   string
	tlsSkipVerify bool
}

// NewGroupVerifier returns a GroupVerifier backed by the real LDAP server(s).
func NewGroupVerifier(primaryURL, backupURL, bindDN, bindPassword, groupBaseDN, groupFilter string, tlsSkipVerify bool) GroupVerifier {
	return &ldapGroupVerifier{
		primaryURL:    primaryURL,
		backupURL:     backupURL,
		bindDN:        bindDN,
		bindPassword:  bindPassword,
		groupBaseDN:   groupBaseDN,
		groupFilter:   groupFilter,
		tlsSkipVerify: tlsSkipVerify,
	}
}

// VerifyGroup returns nil if groupName exists in LDAP, or a descriptive error
// if it does not exist or the LDAP search fails.
func (v *ldapGroupVerifier) VerifyGroup(_ context.Context, groupName string) error {
	conn, err := dialAndBind(v.primaryURL, v.bindDN, v.bindPassword, v.tlsSkipVerify)
	if err != nil {
		primaryErr := err
		if v.backupURL == "" {
			return fmt.Errorf("ldap connect: %w", primaryErr)
		}
		conn, err = dialAndBind(v.backupURL, v.bindDN, v.bindPassword, v.tlsSkipVerify)
		if err != nil {
			return fmt.Errorf("ldap connect: primary: %w; backup: %w", primaryErr, err)
		}
	}
	defer conn.Close()

	filter := fmt.Sprintf("(&%s(cn=%s))", v.groupFilter, goldap.EscapeFilter(groupName))
	req := goldap.NewSearchRequest(
		v.groupBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		1, 0, false,
		filter,
		[]string{"dn"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return fmt.Errorf("ldap group search: %w", err)
	}
	if len(result.Entries) == 0 {
		return fmt.Errorf("group %q not found in LDAP", groupName)
	}
	return nil
}
