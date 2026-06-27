package smtpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"notification_relay/db"
)

// errCRAMCredentialNotFound covers both an unknown username and a disabled
// credential — the two are never distinguished to the client.
var errCRAMCredentialNotFound = errors.New("smtp cram credential not found")

// cramCredentialStore resolves CRAM-MD5 usernames to their plaintext shared
// secret and granted roles. Unlike LDAP, this store owns the secret outright
// (encrypted at rest), since CRAM-MD5 verification requires the server to
// reproduce the client's HMAC using the same key.
type cramCredentialStore struct {
	q   *db.Queries
	key []byte
}

func newCRAMCredentialStore(q *db.Queries, key []byte) *cramCredentialStore {
	return &cramCredentialStore{q: q, key: key}
}

// lookup returns the decrypted secret and roles for username, or
// errCRAMCredentialNotFound if no enabled credential exists.
func (c *cramCredentialStore) lookup(ctx context.Context, username string) (secret string, roles []string, err error) {
	cred, err := c.q.GetCRAMCredential(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, errCRAMCredentialNotFound
		}
		return "", nil, fmt.Errorf("get cram credential: %w", err)
	}

	secret, err = DecryptSecret(c.key, cred.SecretNonce, cred.SecretCipher)
	if err != nil {
		return "", nil, fmt.Errorf("decrypt cram secret: %w", err)
	}

	if err := json.Unmarshal([]byte(cred.Roles), &roles); err != nil {
		return "", nil, fmt.Errorf("parse cram roles: %w", err)
	}

	return secret, roles, nil
}
