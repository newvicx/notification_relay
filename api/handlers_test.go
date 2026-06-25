package api

import "testing"

func TestParseTimeToRFC3339(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"rfc3339", "2026-06-25T14:30:00Z", "2026-06-25T14:30:00Z"},
		{"datetime-local", "2026-06-25T14:30", "2026-06-25T14:30:00Z"},
		{"iso no tz with seconds", "2026-06-25T14:30:00", "2026-06-25T14:30:00Z"},
		{"date only", "2026-06-25", "2026-06-25T00:00:00Z"},
		{"epoch", "1782484200", "2026-06-26T14:30:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTimeToRFC3339(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTimeToRFC3339Invalid(t *testing.T) {
	if _, err := parseTimeToRFC3339("not-a-time"); err == nil {
		t.Fatal("expected error for invalid input")
	}
}
