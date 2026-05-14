package smtpapi

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
)

// extractGroupFromAddr parses a RCPT TO address and returns the local part
// (the LDAP group name) if the domain matches expectedDomain.
func extractGroupFromAddr(addr, expectedDomain string) (string, error) {
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address %q: %w", addr, err)
	}
	parts := strings.SplitN(parsed.Address, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid address %q: missing domain", addr)
	}
	if !strings.EqualFold(parts[1], expectedDomain) {
		return "", fmt.Errorf("recipient domain %q does not match relay domain %q", parts[1], expectedDomain)
	}
	if parts[0] == "" {
		return "", fmt.Errorf("invalid address %q: empty local part", addr)
	}
	return parts[0], nil
}

// extractChannelsFromFromHeader parses the From: message header and returns
// the local parts of addresses whose domain matches expectedDomain.
// Each local part is a delivery channel name (email, sms, voice).
func extractChannelsFromFromHeader(fromHeader, expectedDomain string) ([]string, error) {
	addrs, err := mail.ParseAddressList(fromHeader)
	if err != nil {
		return nil, fmt.Errorf("invalid From header: %w", err)
	}
	var channels []string
	for _, a := range addrs {
		parts := strings.SplitN(a.Address, "@", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[1], expectedDomain) {
			channels = append(channels, strings.ToLower(parts[0]))
		}
	}
	return channels, nil
}

// extractPlainText returns the plain-text body from a mail.Message.
// For multipart/alternative, it prefers text/plain. For non-multipart
// messages it returns the body as-is.
func extractPlainText(msg *mail.Message) (string, error) {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		b, err := io.ReadAll(msg.Body)
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Not parseable — just read raw body.
		b, err := io.ReadAll(msg.Body)
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		b, err := io.ReadAll(msg.Body)
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	var plain, fallback string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		ct := part.Header.Get("Content-Type")
		mt, _, _ := mime.ParseMediaType(ct)
		b, readErr := io.ReadAll(part)
		if readErr != nil {
			continue
		}
		if mt == "text/plain" {
			plain = strings.TrimSpace(string(b))
			break
		}
		if fallback == "" {
			fallback = strings.TrimSpace(string(b))
		}
	}
	if plain != "" {
		return plain, nil
	}
	return fallback, nil
}
