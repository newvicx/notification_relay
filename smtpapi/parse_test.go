package smtpapi

import (
	"reflect"
	"testing"
)

func TestParseRecipient(t *testing.T) {
	const domain = "notification_relay.local"

	tests := []struct {
		name         string
		addr         string
		wantGroup    string
		wantChannels []string
		wantErr      bool
	}{
		{
			name:         "group with multiple channels",
			addr:         "group-notify+sms+email+voice@notification_relay.local",
			wantGroup:    "group-notify",
			wantChannels: []string{"sms", "email", "voice"},
		},
		{
			name:         "single channel",
			addr:         "grp-oncall+email@notification_relay.local",
			wantGroup:    "grp-oncall",
			wantChannels: []string{"email"},
		},
		{
			name:         "no channels (bare group)",
			addr:         "grp-oncall@notification_relay.local",
			wantGroup:    "grp-oncall",
			wantChannels: nil,
		},
		{
			name:         "angle brackets and whitespace are trimmed",
			addr:         "  <grp-oncall+sms@notification_relay.local>  ",
			wantGroup:    "grp-oncall",
			wantChannels: []string{"sms"},
		},
		{
			name:         "channels are lowercased and trimmed",
			addr:         "grp+SMS+ Voice @notification_relay.local",
			wantGroup:    "grp",
			wantChannels: []string{"sms", "voice"},
		},
		{
			name:         "domain match is case-insensitive",
			addr:         "grp+sms@Notification_Relay.Local",
			wantGroup:    "grp",
			wantChannels: []string{"sms"},
		},
		{
			name:         "empty channel entries are skipped",
			addr:         "grp+sms++voice+@notification_relay.local",
			wantGroup:    "grp",
			wantChannels: []string{"sms", "voice"},
		},
		{
			name:         "trailing plus with no channels yields empty set",
			addr:         "grp+@notification_relay.local",
			wantGroup:    "grp",
			wantChannels: nil,
		},
		{
			name:    "wrong domain",
			addr:    "grp+sms@other.local",
			wantErr: true,
		},
		{
			name:    "missing domain",
			addr:    "grp+sms",
			wantErr: true,
		},
		{
			name:    "empty group",
			addr:    "+sms@notification_relay.local",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRecipient(tt.addr, domain)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got group=%q channels=%v", got.group, got.channels)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.group != tt.wantGroup {
				t.Errorf("group = %q, want %q", got.group, tt.wantGroup)
			}
			if !reflect.DeepEqual(got.channels, tt.wantChannels) {
				t.Errorf("channels = %v, want %v", got.channels, tt.wantChannels)
			}
		})
	}
}
