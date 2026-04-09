package notify

// VoiceProvider initiates voice call deliveries via an external provider.
// Call returns the provider-assigned call SID, the initial status string
// reported by the provider, and any error.
type VoiceProvider interface {
	Call(to, message string) (sid, status string, err error)
}
