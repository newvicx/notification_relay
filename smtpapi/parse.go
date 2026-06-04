package smtpapi

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
)

// parsedRecipient is a single RCPT TO recipient decoded into an LDAP group
// target and the delivery channels requested for it.
type parsedRecipient struct {
	group    string
	channels []string
}

// parseRecipient parses a RCPT TO address and returns the LDAP group name and
// any delivery channels encoded in it. The local part has the form
// "group:channel1,channel2" — the group name, optionally followed by a colon
// and a comma-separated list of channels (sms, voice, email). The bare form
// "group" (no colon) is also accepted, leaving channels empty.
//
// The address is parsed manually rather than via net/mail because the ':' and
// ',' separators are not valid in an unquoted RFC 5322 addr-spec.
func parseRecipient(addr, expectedDomain string) (parsedRecipient, error) {
	// go-smtp hands us the bare path, but trim brackets/space defensively.
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "<")
	addr = strings.TrimSuffix(addr, ">")

	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return parsedRecipient{}, fmt.Errorf("invalid address %q: missing domain", addr)
	}
	local, domain := addr[:at], addr[at+1:]
	if !strings.EqualFold(domain, expectedDomain) {
		return parsedRecipient{}, fmt.Errorf("recipient domain %q does not match relay domain %q", domain, expectedDomain)
	}

	group := local
	var channels []string
	if colon := strings.IndexByte(local, ':'); colon >= 0 {
		group = local[:colon]
		// Channels are separated by ',' or '+'. '+' is valid in an unquoted
		// local part, so "group:sms+voice" works even with strict clients.
		fields := strings.FieldsFunc(local[colon+1:], func(r rune) bool {
			return r == ',' || r == '+'
		})
		for _, ch := range fields {
			ch = strings.ToLower(strings.TrimSpace(ch))
			if ch != "" {
				channels = append(channels, ch)
			}
		}
	}
	if group == "" {
		return parsedRecipient{}, fmt.Errorf("invalid address %q: empty group", addr)
	}
	return parsedRecipient{group: group, channels: channels}, nil
}

// extractChannelsFromFromHeader parses the From: message header and returns
// the local parts of addresses whose domain matches expectedDomain.
// Each local part is a delivery channel name (email, sms, voice).
//
// This is a legacy fallback: channels are normally encoded in the RCPT TO
// address (see parseRecipient). It is consulted only when no recipient
// embedded channels, for senders that still set the From: header.
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
