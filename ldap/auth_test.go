package ldap

import (
	"testing"
)

func TestParseCNs_Valid(t *testing.T) {
	dns := []string{
		"CN=grp-oncall,OU=Groups,DC=example,DC=com",
		"CN=grp-admins,OU=Groups,DC=example,DC=com",
	}
	got := parseCNs(dns)
	if len(got) != 2 {
		t.Fatalf("want 2 CNs, got %d: %v", len(got), got)
	}
	if got[0] != "grp-oncall" {
		t.Errorf("got[0] = %q, want %q", got[0], "grp-oncall")
	}
	if got[1] != "grp-admins" {
		t.Errorf("got[1] = %q, want %q", got[1], "grp-admins")
	}
}

func TestParseCNs_Empty(t *testing.T) {
	got := parseCNs([]string{})
	if got == nil {
		t.Error("want empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("want 0 CNs, got %d", len(got))
	}
}

func TestParseCNs_SkipsNonCN(t *testing.T) {
	dns := []string{
		"OU=Groups,DC=example,DC=com", // starts with OU, not CN
		"CN=grp-oncall,OU=Groups,DC=example,DC=com",
	}
	got := parseCNs(dns)
	if len(got) != 1 {
		t.Fatalf("want 1 CN (OU entry skipped), got %d: %v", len(got), got)
	}
	if got[0] != "grp-oncall" {
		t.Errorf("got[0] = %q, want %q", got[0], "grp-oncall")
	}
}

func TestParseCNs_SkipsMalformed(t *testing.T) {
	dns := []string{
		"not a valid dn at all !!!",
		"CN=grp-valid,DC=example,DC=com",
	}
	got := parseCNs(dns)
	// Malformed DNs are silently skipped; only the valid one is returned.
	if len(got) != 1 {
		t.Fatalf("want 1 CN (malformed skipped), got %d: %v", len(got), got)
	}
	if got[0] != "grp-valid" {
		t.Errorf("got[0] = %q, want %q", got[0], "grp-valid")
	}
}

func TestParseCNs_Mixed(t *testing.T) {
	dns := []string{
		"CN=grp-a,DC=example,DC=com",
		"OU=Groups,DC=example,DC=com",
		"CN=grp-b,DC=example,DC=com",
		"not-valid",
		"CN=grp-c,DC=example,DC=com",
	}
	got := parseCNs(dns)
	if len(got) != 3 {
		t.Fatalf("want 3 CNs, got %d: %v", len(got), got)
	}
}
