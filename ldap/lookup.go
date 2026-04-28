package ldap

import (
	"context"
	"errors"
	"fmt"

	goldap "github.com/go-ldap/ldap/v3"
)

// ErrUserNotFound is returned by LookupUser when no LDAP entry matches the username.
var ErrUserNotFound = errors.New("user not found")

// UserLookup fetches LDAP attributes for a single user by sAMAccountName.
// Each call opens its own connection and is safe to call concurrently.
type UserLookup interface {
	LookupUser(ctx context.Context, username string) (*Member, error)
}

type ldapUserLookup struct {
	primaryURL    string
	backupURL     string
	bindDN        string
	bindPassword  string
	userBaseDN    string
	tlsSkipVerify bool
}

// NewUserLookup returns a UserLookup backed by the real LDAP server(s).
func NewUserLookup(primaryURL, backupURL, bindDN, bindPassword, userBaseDN string, tlsSkipVerify bool) UserLookup {
	return &ldapUserLookup{
		primaryURL:    primaryURL,
		backupURL:     backupURL,
		bindDN:        bindDN,
		bindPassword:  bindPassword,
		userBaseDN:    userBaseDN,
		tlsSkipVerify: tlsSkipVerify,
	}
}

// LookupUser searches for a user by sAMAccountName and returns their attributes.
// ErrUserNotFound is returned when the username does not exist in LDAP.
func (l *ldapUserLookup) LookupUser(_ context.Context, username string) (*Member, error) {
	conn, err := dialAndBind(l.primaryURL, l.bindDN, l.bindPassword, l.tlsSkipVerify)
	if err != nil {
		primaryErr := err
		if l.backupURL == "" {
			return nil, fmt.Errorf("ldap connect: %w", primaryErr)
		}
		conn, err = dialAndBind(l.backupURL, l.bindDN, l.bindPassword, l.tlsSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("ldap connect: primary: %w; backup: %w", primaryErr, err)
		}
	}
	defer conn.Close()

	filter := fmt.Sprintf("(sAMAccountName=%s)", goldap.EscapeFilter(username))
	req := goldap.NewSearchRequest(
		l.userBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		1, 0, false,
		filter,
		memberAttrs,
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}
	if len(result.Entries) == 0 {
		return nil, ErrUserNotFound
	}
	entry := result.Entries[0]
	return &Member{
		Username:    entry.GetAttributeValue("sAMAccountName"),
		DisplayName: entry.GetAttributeValue("displayName"),
		Email:       entry.GetAttributeValue("mail"),
		Mobile:      entry.GetAttributeValue("mobile"),
		Work:        entry.GetAttributeValue("telephoneNumber"),
	}, nil
}
