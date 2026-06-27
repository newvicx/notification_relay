package config

import "testing"

func validBaseConfig() *Config {
	cfg := defaults()
	cfg.Database.Path = "test.db"
	cfg.LDAP.PrimaryURL = "ldaps://ldap.example.com"
	cfg.LDAP.BindDN = "CN=svc,DC=example,DC=com"
	cfg.LDAP.BindPassword = "secret"
	cfg.LDAP.UserBaseDN = "OU=Users,DC=example,DC=com"
	return cfg
}

func TestValidate_CRAMMD5RequiresKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SMTPServer.CRAMMD5Enabled = true
	cfg.SMTPServer.CRAMMD5SecretKey = ""
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for missing cram_md5_secret_key")
	}
}

func TestValidate_CRAMMD5RejectsShortKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SMTPServer.CRAMMD5Enabled = true
	// Valid base64, but decodes to fewer than 32 bytes.
	cfg.SMTPServer.CRAMMD5SecretKey = "c2hvcnRrZXk="
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for short cram_md5_secret_key")
	}
}

func TestValidate_CRAMMD5RejectsInvalidBase64(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SMTPServer.CRAMMD5Enabled = true
	cfg.SMTPServer.CRAMMD5SecretKey = "not-valid-base64!!"
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for invalid base64 cram_md5_secret_key")
	}
}

func TestValidate_CRAMMD5AcceptsValid32ByteKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SMTPServer.CRAMMD5Enabled = true
	cfg.SMTPServer.CRAMMD5SecretKey = "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=" // base64 of 32 'a' bytes
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected error for valid 32-byte key: %v", err)
	}
}

func TestValidate_CRAMMD5DisabledIgnoresKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.SMTPServer.CRAMMD5Enabled = false
	cfg.SMTPServer.CRAMMD5SecretKey = ""
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected error when cram_md5 disabled: %v", err)
	}
}
