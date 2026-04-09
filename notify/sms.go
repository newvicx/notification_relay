package notify

// SMSProvider sends SMS messages via an external provider.
// Send returns the provider-assigned message SID, the initial status string
// reported by the provider, and any error.
type SMSProvider interface {
	Send(to, message string) (sid, status string, err error)
}
