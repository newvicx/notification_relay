package notify

// EmailProvider is the interface for sending email deliveries.
// Implementation (SMTP) is a placeholder for the next development phase.
type EmailProvider interface {
	Send(to, subject, body string) error
}
