package ldap

import (
	"context"
	"fmt"

	goldap "github.com/go-ldap/ldap/v3"
)

// Member holds the LDAP attributes for a single group member.
type Member struct {
	Username    string
	DisplayName string
	Email       string
	Mobile      string
	Work        string
}

// Client is the interface the sync loop depends on.
// The real implementation uses go-ldap/v3; tests use an in-memory stub.
type Client interface {
	Connect(ctx context.Context) error
	SearchGroupMembers(ctx context.Context, groupName string, pageSize uint32) ([]Member, error)
	Close() error
}

// ldapClient dials via ldap:// (plaintext) or ldaps:// (TLS) based on the URL
// scheme. tlsSkipVerify only applies to ldaps:// connections.
type ldapClient struct {
	primaryURL    string
	backupURL     string
	bindDN        string
	bindPassword  string
	userBaseDN    string
	groupBaseDN   string
	groupFilter   string
	tlsSkipVerify bool
	conn          goldap.Client
}

// NewClient returns a Client backed by the real LDAP server(s).
// Every Connect call tries primaryURL first; backupURL is used only when the
// primary is unreachable. Pass an empty backupURL if no failover server exists.
func NewClient(primaryURL, backupURL, bindDN, bindPassword, userBaseDN, groupBaseDN, groupFilter string, tlsSkipVerify bool) Client {
	return &ldapClient{
		primaryURL:    primaryURL,
		backupURL:     backupURL,
		bindDN:        bindDN,
		bindPassword:  bindPassword,
		userBaseDN:    userBaseDN,
		groupBaseDN:   groupBaseDN,
		groupFilter:   groupFilter,
		tlsSkipVerify: tlsSkipVerify,
	}
}

// Connect always tries primaryURL first. If the primary fails and backupURL is
// configured, it falls back to the backup. The primary is retried fresh on the
// next call to Connect, so the system naturally returns to the primary once it
// recovers without any persistent failover state.
func (c *ldapClient) Connect(_ context.Context) error {
	conn, err := c.dial(c.primaryURL)
	if err == nil {
		c.conn = conn
		return nil
	}
	primaryErr := err

	if c.backupURL == "" {
		return primaryErr
	}

	conn, err = c.dial(c.backupURL)
	if err != nil {
		return fmt.Errorf("primary: %w; backup: %w", primaryErr, err)
	}
	c.conn = conn
	return nil
}

func (c *ldapClient) dial(url string) (goldap.Client, error) {
	return dialAndBind(url, c.bindDN, c.bindPassword, c.tlsSkipVerify)
}

// memberAttrs is the fixed set of user attributes fetched on every sync.
var memberAttrs = []string{"sAMAccountName", "displayName", "mail", "mobile", "telephoneNumber"}

// SearchGroupMembers searches LDAP for direct members of groupName using a
// memberOf filter under groupBaseDN. pageSize controls paged result set size.
func (c *ldapClient) SearchGroupMembers(_ context.Context, groupName string, pageSize uint32) ([]Member, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected: call Connect first")
	}

	// Resolve the group DN from its CN so we can filter by memberOf.
	groupDN, err := c.resolveGroupDN(groupName)
	if err != nil {
		return nil, fmt.Errorf("resolve group DN for %q: %w", groupName, err)
	}

	filter := fmt.Sprintf("(&(objectClass=user)(memberOf=%s))", goldap.EscapeFilter(groupDN))

	req := goldap.NewSearchRequest(
		c.userBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		0, 0, false,
		filter,
		memberAttrs,
		nil,
	)

	result, err := c.conn.SearchWithPaging(req, pageSize)
	if err != nil {
		return nil, fmt.Errorf("search members of %q: %w", groupName, err)
	}

	members := make([]Member, 0, len(result.Entries))
	for _, entry := range result.Entries {
		members = append(members, Member{
			Username:    entry.GetAttributeValue("sAMAccountName"),
			DisplayName: entry.GetAttributeValue("displayName"),
			Email:       entry.GetAttributeValue("mail"),
			Mobile:      entry.GetAttributeValue("mobile"),
			Work:        entry.GetAttributeValue("telephoneNumber"),
		})
	}
	return members, nil
}

func (c *ldapClient) resolveGroupDN(groupName string) (string, error) {
	filter := fmt.Sprintf("(&%s(cn=%s))", c.groupFilter, goldap.EscapeFilter(groupName))
	req := goldap.NewSearchRequest(
		c.groupBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		1, 0, false,
		filter,
		[]string{"dn"},
		nil,
	)
	result, err := c.conn.Search(req)
	if err != nil {
		return "", err
	}
	if len(result.Entries) == 0 {
		return "", fmt.Errorf("group %q not found in LDAP", groupName)
	}
	return result.Entries[0].DN, nil
}

func (c *ldapClient) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}
