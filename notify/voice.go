package notify

// VoiceProvider is the interface for initiating voice call deliveries.
// Implementation (Twilio) is a placeholder for the next development phase.
type VoiceProvider interface {
	Call(to, message string) (sid string, err error)
}
