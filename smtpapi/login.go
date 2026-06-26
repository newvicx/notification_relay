package smtpapi

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// loginAuthenticator verifies a username/password pair, mirroring
// sasl.PlainAuthenticator but without an authorization identity (LOGIN has
// none).
type loginAuthenticator func(username, password string) error

// loginServer implements the SASL LOGIN mechanism server side. go-sasl only
// ships a LOGIN client (it's considered obsolete in favor of PLAIN), so the
// two-step "Username:" / "Password:" challenge exchange is implemented here.
type loginServer struct {
	step         int
	username     string
	authenticate loginAuthenticator
}

func newLoginServer(authenticator loginAuthenticator) sasl.Server {
	return &loginServer{authenticate: authenticator}
}

func (s *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	// No initial response: ask for the username first.
	if s.step == 0 && response == nil {
		s.step = 1
		return []byte("Username:"), false, nil
	}

	switch s.step {
	case 0, 1:
		// step 0 with a non-nil response is an initial response carrying the
		// username directly, so both cases just need the password next.
		s.username = string(response)
		s.step = 2
		return []byte("Password:"), false, nil
	case 2:
		s.step = 3
		err = s.authenticate(s.username, string(response))
		return nil, true, err
	default:
		return nil, false, errors.New("sasl: unexpected client response")
	}
}
