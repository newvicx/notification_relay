//go:build integration

package notify_test

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// capturedEmail holds the envelope and raw DATA content from a single SMTP transaction.
type capturedEmail struct {
	From    string
	To      []string
	RawData string // everything between DATA and the terminating "."
}

// fakeSMTP is a minimal in-process SMTP server for integration testing.
// It speaks just enough of the protocol for net/smtp to complete a successful send.
type fakeSMTP struct {
	addr     string
	mu       sync.Mutex
	messages []capturedEmail
	listener net.Listener
	wg       sync.WaitGroup
}

// startFakeSMTP starts a fake SMTP server on a random loopback port.
// The server is automatically stopped when the test ends.
func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeSMTP: listen: %v", err)
	}
	s := &fakeSMTP{addr: ln.Addr().String(), listener: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.serve()
	}()
	t.Cleanup(s.stop)
	return s
}

// stop closes the listener and waits for all connection goroutines to exit.
func (s *fakeSMTP) stop() {
	s.listener.Close()
	s.wg.Wait()
}

// received returns a snapshot of captured messages.
func (s *fakeSMTP) received() []capturedEmail {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedEmail, len(s.messages))
	copy(out, s.messages)
	return out
}

// port parses the port number from s.addr ("host:port").
func (s *fakeSMTP) port() int {
	_, portStr, _ := net.SplitHostPort(s.addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

func (s *fakeSMTP) handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)

	send := func(line string) {
		fmt.Fprintf(conn, "%s\r\n", line)
	}

	send("220 localhost fakeSMTP")

	var msg capturedEmail

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			send("250-localhost")
			send("250-AUTH PLAIN LOGIN")
			send("250 OK")

		case strings.HasPrefix(upper, "AUTH"):
			// Accept any AUTH attempt without validating credentials.
			send("235 Authentication successful")

		case strings.HasPrefix(upper, "MAIL FROM"):
			addr := extractAngle(line)
			msg.From = addr
			send("250 OK")

		case strings.HasPrefix(upper, "RCPT TO"):
			addr := extractAngle(line)
			msg.To = append(msg.To, addr)
			send("250 OK")

		case upper == "DATA":
			send("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(dataLine, "\r\n")
				if trimmed == "." {
					break
				}
				// Unstuff leading dots per RFC 5321.
				if strings.HasPrefix(trimmed, "..") {
					trimmed = trimmed[1:]
				}
				body.WriteString(trimmed + "\n")
			}
			msg.RawData = body.String()
			s.mu.Lock()
			s.messages = append(s.messages, msg)
			s.mu.Unlock()
			msg = capturedEmail{} // reset for potential pipelining
			send("250 OK")

		case upper == "QUIT":
			send("221 Bye")
			return

		case upper == "RSET":
			msg = capturedEmail{}
			send("250 OK")

		default:
			send("500 Unrecognized command")
		}
	}
}

// extractAngle pulls the address out of "MAIL FROM:<addr>" or "RCPT TO:<addr>".
func extractAngle(line string) string {
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start >= 0 && end > start {
		return line[start+1 : end]
	}
	return line
}
