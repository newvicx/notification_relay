package notify

// SMSProvider is the interface for sending SMS deliveries.
// Implementation (Twilio) is a placeholder for the next development phase.
type SMSProvider interface {
	Send(to, message string) (sid string, err error)
}
