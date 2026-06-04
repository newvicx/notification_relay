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
// "group+channel1+channel2" — the group name, optionally followed by one or
// more '+'-separated channels (sms, voice, email). The bare form "group" (no
// '+') is accepted too, leaving channels empty.
//
// '+' is used because it is a valid character in an unquoted RFC 5322 local
// part, so the address survives strict SMTP clients and intermediaries.
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

	// The first '+'-separated field is the group; the rest are channels.
	fields := strings.Split(local, "+")
	group := strings.TrimSpace(fields[0])
	var channels []string
	seen := make(map[string]bool)
	for _, ch := range fields[1:] {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if ch != "" && !seen[ch] {
			seen[ch] = true
			channels = append(channels, ch)
		}
	}
	if group == "" {
		return parsedRecipient{}, fmt.Errorf("invalid address %q: empty group", addr)
	}
	return parsedRecipient{group: group, channels: channels}, nil
}

// missingChannelError reports a recipient group that ended up with no delivery
// channels (none embedded and no From: fallback available).
type missingChannelError struct{ group string }

func (e missingChannelError) Error() string {
	return fmt.Sprintf("no channel specified for group %q", e.group)
}

// resolveTargets pairs each recipient with its effective delivery channels: the
// channels embedded in the recipient address, or the fallback (From: header)
// channels when the recipient embedded none. It returns a missingChannelError
// for the first recipient that is left with no channels. The returned slice is
// independent of the inputs.
func resolveTargets(recipients []parsedRecipient, fallback []string) ([]parsedRecipient, error) {
	resolved := make([]parsedRecipient, 0, len(recipients))
	for _, rcpt := range recipients {
		channels := rcpt.channels
		if len(channels) == 0 {
			channels = fallback
		}
		if len(channels) == 0 {
			return nil, missingChannelError{group: rcpt.group}
		}
		resolved = append(resolved, parsedRecipient{group: rcpt.group, channels: channels})
	}
	return resolved, nil
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
