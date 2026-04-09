package ldap

import (
	"crypto/tls"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// dialAndBind opens a connection to url and binds with the given credentials.
// For ldaps:// URLs TLS is configured with optional InsecureSkipVerify;
// for ldap:// URLs the connection is plaintext and no TLS config is applied.
func dialAndBind(url, bindDN, bindPassword string, tlsSkipVerify bool) (goldap.Client, error) {
	var opts []goldap.DialOpt
	if strings.HasPrefix(url, "ldaps://") {
		opts = append(opts, goldap.DialWithTLSConfig(&tls.Config{
			InsecureSkipVerify: tlsSkipVerify, //nolint:gosec
		}))
	}
	conn, err := goldap.DialURL(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	if err := conn.Bind(bindDN, bindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bind on %s as %s: %w", url, bindDN, err)
	}
	return conn, nil
}
