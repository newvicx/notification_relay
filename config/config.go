package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database   DatabaseConfig   `yaml:"database"`
	HTTP       HTTPConfig       `yaml:"http"`
	LDAP       LDAPConfig       `yaml:"ldap"`
	OAuth      OAuthConfig      `yaml:"oauth"`
	Twilio     TwilioConfig     `yaml:"twilio"`
	SMTP       SMTPConfig       `yaml:"smtp"`
	SMTPServer SMTPServerConfig `yaml:"smtp_server"`
	Notify     NotifyConfig     `yaml:"notify"`
	EventSweep EventSweepConfig `yaml:"event_sweep"`
	Logging    LoggingConfig    `yaml:"logging"`
	Severities []string         `yaml:"severities"`
}

type DatabaseConfig struct {
	Path           string `yaml:"path"`
	MaxReaderConns int    `yaml:"max_reader_conns"`
}

type HTTPConfig struct {
	ListenAddr      string        `yaml:"listen_addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	SpecPath        string        `yaml:"spec_path"`
	// PathPrefix is prepended to every server-generated redirect and to every
	// UI resource link (stylesheet, script, form action, htmx endpoint) so the
	// app works behind a reverse proxy that maps it to a subpath, e.g.
	// "/relay". The proxy is expected to strip the prefix before forwarding
	// requests, so route matching itself is unaffected — only the URLs the
	// server writes back to the browser change. Must start with "/" and must
	// not end with "/"; empty (default) means no prefix.
	PathPrefix string `yaml:"path_prefix"`
}

type LDAPConfig struct {
	// PrimaryURL is the preferred LDAP server. Every connection attempt starts
	// here before falling back to BackupURL. Use ldap:// for plaintext or
	// ldaps:// for TLS — the scheme determines whether TLS is used.
	PrimaryURL string `yaml:"primary_url"`
	// BackupURL is optional. When set, the client falls back to this server if
	// the primary is unreachable. The primary is retried on the next sync cycle.
	// Must use the same scheme as PrimaryURL.
	BackupURL string `yaml:"backup_url"`
	// BindDN is the distinguished name used to authenticate the service account,
	// e.g. "CN=svc-relay,OU=ServiceAccounts,DC=corp,DC=example,DC=com".
	BindDN string `yaml:"bind_dn"`
	// BindPassword is the password for the service account. Use ${ENV_VAR} to
	// avoid storing credentials in the config file.
	BindPassword string `yaml:"bind_password"`
	// UserBaseDN is the root of the directory tree searched when resolving group
	// members. Member searches scope the entire subtree beneath this DN, so it
	// should be the top of the user OU (or the domain root), not a group OU.
	// Example: "OU=Users,DC=corp,DC=example,DC=com"
	UserBaseDN string `yaml:"user_base_dn"`
	// GroupBaseDN is the root of the directory tree searched when resolving a
	// group CN to its full distinguished name. Scope this to the OU that contains
	// the groups used for roles and LDAP sync.
	// Example: "OU=Groups,DC=corp,DC=example,DC=com"
	GroupBaseDN string `yaml:"group_base_dn"`
	// GroupFilter is the LDAP object-class filter used when searching for groups.
	// It is ANDed with a CN match, so only the class predicate is needed here.
	// Default: "(objectClass=group)"
	GroupFilter string `yaml:"group_filter"`
	// Roles maps role names to the LDAP group CNs whose members receive that role.
	// Valid role names are: admin, publisher, reader. A user's effective roles are
	// the union of all roles for which they are a member of at least one listed group.
	// Example:
	//   roles:
	//     admin:     [grp-relay-admins]
	//     publisher: [grp-oncall, grp-operations]
	//     reader:    [grp-monitoring]
	Roles        map[string][]string `yaml:"roles"`
	SyncInterval time.Duration       `yaml:"sync_interval"`
	DialTimeout  time.Duration       `yaml:"dial_timeout"`
	// TLSSkipVerify disables certificate verification for ldaps:// connections.
	// Has no effect when using ldap:// (plaintext).
	TLSSkipVerify bool `yaml:"tls_skip_verify"`
	// AuthCacheSize is the maximum number of authenticated users to cache in the
	// in-process LRU. Set to 0 to disable caching. Default: 256.
	AuthCacheSize int `yaml:"auth_cache_size"`
	// AuthCacheTTL controls how long a cached authentication result remains valid.
	// Keep short to ensure group changes take effect promptly. Default: 30s.
	AuthCacheTTL time.Duration `yaml:"auth_cache_ttl"`
	PageSize     uint32        `yaml:"page_size"`
}

// OAuthConfig enables OpenID Connect / OAuth 2.0 authentication against an
// external identity provider. It handles authentication only: the verified
// identity claim is matched to an LDAP account and roles still come from
// ldap.roles group membership.
type OAuthConfig struct {
	// Enabled turns on JWT bearer authentication for the API and the
	// authorization-code login flow for the web UI. Default: false.
	Enabled bool `yaml:"enabled"`
	// Issuer is the provider's issuer URL, e.g. "https://sso.example.com".
	// Endpoints and signing keys are discovered from
	// <issuer>/.well-known/openid-configuration. Must match the "iss" claim.
	Issuer string `yaml:"issuer"`
	// ClientID and ClientSecret identify this application to the provider
	// (confidential client). ClientSecret may be empty for a public client
	// using PKCE only. The secret is read from the config (use ${ENV_VAR}
	// rather than a literal), kept in memory, never logged, and sent only to
	// the provider's token endpoint; it is not stored in the database. When
	// set, issuer must be https:// so it is never sent in cleartext.
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// RedirectURL is the externally visible callback URL registered with the
	// provider, including any http.path_prefix, e.g.
	// "https://relay.example.com/relay/ui/oauth/callback".
	RedirectURL string `yaml:"redirect_url"`
	// Scopes requested during the UI login. "openid" is always included.
	// Default: [openid, profile].
	Scopes []string `yaml:"scopes"`
	// Audience is the value API bearer tokens must carry in their "aud"
	// claim. Default: client_id.
	Audience string `yaml:"audience"`
	// IdentityClaim is the JWT claim identifying the user, whatever the
	// provider sends (username, email, ...). It is matched against the user's
	// LDAP sAMAccountName, userPrincipalName or mail to find their groups.
	// Default: "sub".
	IdentityClaim string `yaml:"identity_claim"`
	// SigningAlgs restricts accepted JWT signature algorithms. Default: [RS256].
	SigningAlgs []string `yaml:"signing_algs"`
	// ProviderName is the label shown on the UI sign-in button.
	// Default: "single sign-on".
	ProviderName string `yaml:"provider_name"`
	// TLSSkipVerify disables certificate verification when talking to the
	// provider. For testing only.
	TLSSkipVerify bool `yaml:"tls_skip_verify"`
	// GroupCacheTTL controls how long LDAP group lookups for bearer-token
	// users are cached, so group changes apply promptly. Default: 30s.
	GroupCacheTTL time.Duration `yaml:"group_cache_ttl"`
}

type TwilioConfig struct {
	AccountSID string `yaml:"account_sid"`
	TokenSID   string `yaml:"token_sid"`
	AuthToken  string `yaml:"auth_token"`
	FromNumber string `yaml:"from_number"`
	// PollInterval controls how often the poller checks Twilio for delivery status.
	PollInterval time.Duration `yaml:"poll_interval"`
	// PollAttemptLimit is the maximum number of failed poll attempts before a
	// delivery is marked as "poll_failed" and removed from the polling queue.
	// Set to 0 to disable the limit. Default: 10.
	PollAttemptLimit int `yaml:"poll_attempt_limit"`
}

type SMTPConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	FromAddress string `yaml:"from_address"`
	// TLSMode controls how the SMTP connection is secured:
	//   "none"     — plaintext (not recommended outside trusted networks)
	//   "starttls" — connect in plaintext, then upgrade via STARTTLS (port 587)
	//   "tls"      — connect directly over TLS from the start (port 465)
	TLSMode string `yaml:"tls_mode"`
}

type SMTPServerConfig struct {
	// ListenAddr is the address the SMTP server listens on, e.g. ":2525".
	// Empty string disables the server (default).
	ListenAddr string `yaml:"listen_addr"`
	// Domain is the accepted relay domain, e.g. "relay.local".
	// RCPT TO addresses must match this domain; their local part encodes the
	// group and delivery channels as "group+sms+voice" (email, sms, voice).
	Domain string `yaml:"domain"`
	// MaxMessageBytes caps the size of inbound messages. Default: 1 MB.
	MaxMessageBytes int64 `yaml:"max_message_bytes"`
	// TLSMode controls how the inbound SMTP connection is secured:
	//   "none"     — plaintext only (default)
	//   "starttls" — accept plaintext connections, upgrade via STARTTLS
	//   "tls"      — require TLS from the first byte (implicit TLS, like SMTPS)
	TLSMode     string `yaml:"tls_mode"`
	TLSCertFile string `yaml:"tls_cert_file"`
	TLSKeyFile  string `yaml:"tls_key_file"`
	// CRAMMD5Enabled turns on the SASL CRAM-MD5 mechanism, authenticated
	// against a separate credential store (managed via the
	// /api/v1/smtp/cram-credentials endpoints) rather than LDAP — CRAM-MD5
	// requires the server to hold the plaintext shared secret, which an LDAP
	// bind never exposes.
	CRAMMD5Enabled bool `yaml:"cram_md5_enabled"`
	// CRAMMD5SecretKey is a base64-encoded 32-byte AES-256 key used to encrypt
	// CRAM-MD5 credential secrets at rest. Required when CRAMMD5Enabled is true.
	CRAMMD5SecretKey string `yaml:"cram_md5_secret_key"`
}

type NotifyConfig struct {
	WorkerCount     int           `yaml:"worker_count"`
	RetryLimit      int           `yaml:"retry_limit"`
	RetryDelay      time.Duration `yaml:"retry_delay"`
	DeliveryTimeout time.Duration `yaml:"delivery_timeout"`
	// DeliveryConcurrency caps the number of in-flight per-channel delivery
	// goroutines across all workers. Prevents unbounded goroutine growth when
	// large groups are targeted or retries pile up. Default: 16.
	DeliveryConcurrency int `yaml:"delivery_concurrency"`
}

type EventSweepConfig struct {
	// Enabled turns on the background sweep that auto-closes events left open
	// (end_time IS NULL) past TTL. Disabled by default: closing an event is a
	// claim about reality ("this stopped"), and silently making that claim on
	// a timer is a behavior change operators should opt into deliberately.
	Enabled bool `yaml:"enabled"`
	// TTL is how long an event may stay open before the sweep auto-closes it.
	// Measured from the event's start_time. Default: 24h.
	TTL time.Duration `yaml:"ttl"`
	// Interval controls how often the sweep runs. Default: 15m.
	Interval time.Duration `yaml:"interval"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	// Dir is the directory where log files are written with daily rotation
	// (e.g. "2006-01-02.log"). When empty, logs are written to stdout.
	Dir string `yaml:"dir"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	data = interpolateEnv(data)

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// interpolateEnv replaces every ${VAR} occurrence in the raw YAML bytes with
// the value of the named environment variable. If the variable is not set the
// placeholder is left as-is so that validate() can catch missing required values
// with a clear error message.
var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func interpolateEnv(data []byte) []byte {
	return envPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		name := string(match[2 : len(match)-1]) // strip ${ and }
		if val, ok := os.LookupEnv(name); ok {
			return []byte(val)
		}
		return match // leave unresolved placeholder intact
	})
}

func defaults() *Config {
	return &Config{
		Database: DatabaseConfig{
			MaxReaderConns: 4,
		},
		HTTP: HTTPConfig{
			ListenAddr:      ":8080",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			SpecPath:        "openapi.yaml",
		},
		LDAP: LDAPConfig{
			GroupFilter:   "(objectClass=group)",
			SyncInterval:  15 * time.Minute,
			DialTimeout:   10 * time.Second,
			TLSSkipVerify: false,
			PageSize:      500,
			AuthCacheSize: 256,
			AuthCacheTTL:  30 * time.Second,
		},
		OAuth: OAuthConfig{
			Scopes:        []string{"openid", "profile"},
			IdentityClaim: "sub",
			SigningAlgs:   []string{"RS256"},
			ProviderName:  "single sign-on",
			GroupCacheTTL: 30 * time.Second,
		},
		Twilio: TwilioConfig{
			PollInterval:     30 * time.Second,
			PollAttemptLimit: 10,
		},
		SMTP: SMTPConfig{
			TLSMode: "starttls",
		},
		SMTPServer: SMTPServerConfig{
			MaxMessageBytes: 1 << 20, // 1 MB
			TLSMode:         "none",
		},
		Notify: NotifyConfig{
			WorkerCount:         4,
			RetryLimit:          3,
			RetryDelay:          15 * time.Second,
			DeliveryTimeout:     30 * time.Second,
			DeliveryConcurrency: 16,
		},
		EventSweep: EventSweepConfig{
			TTL:      24 * time.Hour,
			Interval: 15 * time.Minute,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Severities: []string{"None", "Information", "Warning", "Minor", "Major", "Critical"},
	}
}

func validate(cfg *Config) error {
	if cfg.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	if cfg.LDAP.PrimaryURL == "" {
		return fmt.Errorf("ldap.primary_url is required")
	}
	if cfg.LDAP.BindDN == "" {
		return fmt.Errorf("ldap.bind_dn is required")
	}
	if cfg.LDAP.BindPassword == "" {
		return fmt.Errorf("ldap.bind_password is required")
	}
	if cfg.LDAP.UserBaseDN == "" {
		return fmt.Errorf("ldap.user_base_dn is required")
	}
	if cfg.HTTP.PathPrefix != "" {
		if !strings.HasPrefix(cfg.HTTP.PathPrefix, "/") || strings.HasSuffix(cfg.HTTP.PathPrefix, "/") {
			return fmt.Errorf("http.path_prefix must start with \"/\" and not end with \"/\" (got %q)", cfg.HTTP.PathPrefix)
		}
	}
	if cfg.OAuth.Enabled {
		o := &cfg.OAuth
		if o.Audience == "" {
			o.Audience = o.ClientID
		}
		if o.Issuer == "" || o.ClientID == "" || o.RedirectURL == "" {
			return fmt.Errorf("oauth.issuer, oauth.client_id and oauth.redirect_url are required when oauth.enabled is true")
		}
		if !strings.HasPrefix(o.Issuer, "https://") && !strings.HasPrefix(o.Issuer, "http://") {
			return fmt.Errorf("oauth.issuer must be an http(s) URL (got %q)", o.Issuer)
		}
		if !strings.HasPrefix(o.RedirectURL, "https://") && !strings.HasPrefix(o.RedirectURL, "http://") {
			return fmt.Errorf("oauth.redirect_url must be an absolute http(s) URL (got %q)", o.RedirectURL)
		}
		if o.ClientSecret != "" && !strings.HasPrefix(o.Issuer, "https://") {
			return fmt.Errorf("oauth.issuer must be https:// when oauth.client_secret is set")
		}
		if o.Audience == "" {
			return fmt.Errorf("oauth.audience or oauth.client_id is required when oauth.enabled is true")
		}
	}
	switch cfg.SMTP.TLSMode {
	case "none", "starttls", "tls", "":
	default:
		return fmt.Errorf("smtp.tls_mode must be one of: none, starttls, tls (got %q)", cfg.SMTP.TLSMode)
	}
	if cfg.SMTPServer.ListenAddr != "" && cfg.SMTPServer.Domain == "" {
		return fmt.Errorf("smtp_server.domain is required when smtp_server.listen_addr is set")
	}
	switch cfg.SMTPServer.TLSMode {
	case "none", "starttls", "tls", "":
	default:
		return fmt.Errorf("smtp_server.tls_mode must be one of: none, starttls, tls (got %q)", cfg.SMTPServer.TLSMode)
	}
	if cfg.SMTPServer.TLSMode == "starttls" || cfg.SMTPServer.TLSMode == "tls" {
		if cfg.SMTPServer.TLSCertFile == "" || cfg.SMTPServer.TLSKeyFile == "" {
			return fmt.Errorf("smtp_server.tls_cert_file and smtp_server.tls_key_file are required when smtp_server.tls_mode is %q", cfg.SMTPServer.TLSMode)
		}
	}
	if cfg.SMTPServer.CRAMMD5Enabled {
		key, err := base64.StdEncoding.DecodeString(cfg.SMTPServer.CRAMMD5SecretKey)
		if err != nil || len(key) != 32 {
			return fmt.Errorf("smtp_server.cram_md5_secret_key must be a base64-encoded 32-byte key when smtp_server.cram_md5_enabled is true")
		}
	}
	if cfg.EventSweep.Enabled && cfg.EventSweep.TTL <= 0 {
		return fmt.Errorf("event_sweep.ttl must be positive when event_sweep.enabled is true")
	}
	knownRoles := map[string]bool{"admin": true, "publisher": true, "reader": true}
	for name := range cfg.LDAP.Roles {
		if !knownRoles[name] {
			return fmt.Errorf("ldap.roles: unknown role %q (must be one of: admin, publisher, reader)", name)
		}
	}
	return nil
}
