package ldap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// AuthResult holds the outcome of a successful credential verification.
type AuthResult struct {
	UserDN string   // full distinguished name of the authenticated user
	Groups []string // CN values of all direct LDAP groups the user is a member of
}

// ErrInvalidCredentials is returned when the username does not exist or the
// password is wrong. Callers should map this to HTTP 401.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Authenticator verifies user credentials against LDAP and resolves their
// group memberships. Each call opens, uses, and closes its own LDAP connection
// and is safe to call concurrently from multiple HTTP request goroutines.
type Authenticator interface {
	AuthenticateUser(ctx context.Context, username, password string) (*AuthResult, error)
}

type ldapAuthenticator struct {
	primaryURL    string
	backupURL     string
	bindDN        string
	bindPassword  string
	userBaseDN    string
	tlsSkipVerify bool
}

// NewAuthenticator returns an Authenticator backed by the real LDAP server(s).
func NewAuthenticator(primaryURL, backupURL, bindDN, bindPassword, userBaseDN string, tlsSkipVerify bool) Authenticator {
	return &ldapAuthenticator{
		primaryURL:    primaryURL,
		backupURL:     backupURL,
		bindDN:        bindDN,
		bindPassword:  bindPassword,
		userBaseDN:    userBaseDN,
		tlsSkipVerify: tlsSkipVerify,
	}
}

// AuthenticateUser verifies the user's credentials and returns their LDAP group
// memberships. The auth flow is:
//  1. Bind as service account to find the user's DN and memberOf attribute.
//  2. Rebind on the same connection as the user to verify their password.
//  3. Parse group CNs from the memberOf DNs and return them.
//
// ErrInvalidCredentials is returned for both unknown usernames and wrong
// passwords to avoid revealing whether a username exists.
func (a *ldapAuthenticator) AuthenticateUser(_ context.Context, username, password string) (*AuthResult, error) {
	conn, err := dialAndBind(a.primaryURL, a.bindDN, a.bindPassword, a.tlsSkipVerify)
	if err != nil {
		primaryErr := err
		if a.backupURL == "" {
			return nil, fmt.Errorf("ldap connect: %w", primaryErr)
		}
		conn, err = dialAndBind(a.backupURL, a.bindDN, a.bindPassword, a.tlsSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("ldap connect: primary: %w; backup: %w", primaryErr, err)
		}
	}
	defer conn.Close()

	// Search for the user by sAMAccountName and fetch their memberOf attribute.
	filter := fmt.Sprintf("(sAMAccountName=%s)", goldap.EscapeFilter(username))
	req := goldap.NewSearchRequest(
		a.userBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		1, 0, false,
		filter,
		[]string{"dn", "memberOf"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}
	if len(result.Entries) == 0 {
		// Return ErrInvalidCredentials so callers cannot distinguish "user not
		// found" from "wrong password" via the error value.
		return nil, ErrInvalidCredentials
	}
	entry := result.Entries[0]
	memberOfDNs := entry.GetAttributeValues("memberOf")

	// Rebind as the user to verify their password.
	if err := conn.Bind(entry.DN, password); err != nil {
		if goldap.IsErrorWithCode(err, goldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("user bind: %w", err)
	}

	return &AuthResult{
		UserDN: entry.DN,
		Groups: parseCNs(memberOfDNs),
	}, nil
}

// parseCNs extracts the CN value from a slice of LDAP distinguished names.
// DNs that do not begin with a CN RDN or cannot be parsed are silently skipped.
func parseCNs(dns []string) []string {
	cns := make([]string, 0, len(dns))
	for _, dn := range dns {
		parsed, err := goldap.ParseDN(dn)
		if err != nil || len(parsed.RDNs) == 0 || len(parsed.RDNs[0].Attributes) == 0 {
			continue
		}
		attr := parsed.RDNs[0].Attributes[0]
		if !strings.EqualFold(attr.Type, "CN") {
			continue
		}
		cns = append(cns, attr.Value)
	}
	return cns
}
