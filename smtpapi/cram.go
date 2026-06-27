package smtpapi

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// cramLookup resolves a CRAM-MD5 username to its plaintext shared secret and
// granted roles. errCRAMCredentialNotFound indicates an invalid username.
type cramLookup func(ctx context.Context, username string) (secret string, roles []string, err error)

// cramFinish is invoked once the digest has been verified; it applies the
// same permission check and audit logging as the LDAP-backed mechanisms.
type cramFinish func(username string, roles []string) error

// cramFail is invoked when authentication is rejected (unknown username or
// bad digest), so the caller can audit-log the failed attempt the same way
// the LDAP path does. username is "" if the response couldn't be parsed.
type cramFail func(username string)

// cramServer implements the SASL CRAM-MD5 mechanism server side (RFC 2195).
// go-sasl ships no CRAM-MD5 implementation, same situation as LOGIN
// (see login.go): a single challenge/response round trip, hand-rolled here.
type cramServer struct {
	domain    string
	challenge string
	lookup    cramLookup
	finish    cramFinish
	fail      cramFail
}

func newCRAMServer(domain string, lookup cramLookup, finish cramFinish, fail cramFail) sasl.Server {
	return &cramServer{domain: domain, lookup: lookup, finish: finish, fail: fail}
}

func (s *cramServer) Next(response []byte) (challenge []byte, done bool, err error) {
	if response == nil {
		s.challenge = generateCRAMChallenge(s.domain)
		return []byte(s.challenge), false, nil
	}

	username, digest, err := parseCRAMResponse(response)
	if err != nil {
		s.fail("")
		return nil, true, invalidCRAMCredentialsError()
	}

	secret, roles, err := s.lookup(context.Background(), username)
	if err != nil {
		if errors.Is(err, errCRAMCredentialNotFound) {
			s.fail(username)
			return nil, true, invalidCRAMCredentialsError()
		}
		return nil, true, smtpInternalError()
	}

	mac := hmac.New(md5.New, []byte(secret))
	mac.Write([]byte(s.challenge))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(digest)) {
		s.fail(username)
		return nil, true, invalidCRAMCredentialsError()
	}

	return nil, true, s.finish(username, roles)
}

func invalidCRAMCredentialsError() *gosmtp.SMTPError {
	return &gosmtp.SMTPError{
		Code:         535,
		EnhancedCode: gosmtp.EnhancedCode{5, 7, 8},
		Message:      "Authentication credentials invalid",
	}
}

// generateCRAMChallenge returns an RFC 2195-style challenge:
// "<random-hex.timestamp@domain>".
func generateCRAMChallenge(domain string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("smtpapi: crypto/rand unavailable: " + err.Error())
	}
	return fmt.Sprintf("<%s.%d@%s>", hex.EncodeToString(buf[:]), time.Now().UnixNano(), domain)
}

// parseCRAMResponse splits a "username hexdigest" response.
func parseCRAMResponse(response []byte) (username, digest string, err error) {
	parts := strings.SplitN(string(response), " ", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("sasl: malformed CRAM-MD5 response")
	}
	return parts[0], parts[1], nil
}
