# notification_relay

A notification delivery relay that sends alerts through Email, SMS, and Voice to on-call groups, with LDAP-based membership syncing and RBAC.

## Build & Run

```bash
# Build server
go build -o notification_relay ./cmd/notification_relay

# Build CLI
go build -o nrcli ./cmd/nrcli

# Run
./notification_relay -config config.yaml

# Generate SQLc types (after modifying SQL queries)
sqlc generate

# Run migrations (auto-runs on startup via Goose)
# Migrations live in db/migrations/
```

## Project Structure

```
cmd/notification_relay/  # Server entry point (main.go)
cmd/nrcli/               # CLI client entry point and subcommands
api/                     # HTTP server, routes, middleware, auth, SMS subscribe form
config/                  # YAML config loading with env var interpolation
db/                      # SQLite connections, SQLc-generated queries
  migrations/            # Goose migration files
ldap/                    # LDAP client, authentication, group sync
notify/                  # Dispatcher, delivery providers (email/SMS/voice), Twilio poller, event auto-expire sweep, templating
smtpapi/                 # SMTP ingestion server (email-to-notification gateway)
sql/                     # Schema and SQLc query definitions
```

## Key Architecture Notes

**Startup sequence** (`cmd/notification_relay/main.go`):
1. Load config → open SQLite (writer + reader pool) → run migrations
2. Start goroutines: LDAP syncer, Twilio poller, notification dispatcher, event auto-expire sweep (if `event_sweep.enabled`), HTTP server, and (if `smtp_server.listen_addr` is set) the SMTP ingestion server
3. Graceful shutdown on SIGINT/SIGTERM

**Database**: SQLite with WAL mode. Single writer connection (avoids `SQLITE_BUSY`), configurable reader pool (default 4). All queries are SQLc-generated — edit `sql/` then run `sqlc generate`.

**Authentication**: HTTP Basic Auth verified against LDAP, with an LRU cache (default 256 entries, 30s TTL). Group→Role mapping is configured in `config.yaml` under `ldap.roles`.

**RBAC roles**:
- `admin` — full access
- `publisher` — publish + read
- `reader` — read-only

**LDAP sync**: Periodic full delete+reinsert per configured group (default 15m). Membership stored in `group_members` table.

**Notification flow**: Event → Notification (targets groups) → expand to members → Delivery records per member per channel → dispatcher workers process queue. Delivery providers: SMTP for email, Twilio for SMS and voice. Email/SMS bodies can reference a stored template (`api/templates.go`, `notify/template.go`) rendered with `html/template`.

**Twilio status**: Polled periodically (default 30s) as a webhook fallback due to firewall restrictions (`notify/poller.go`).

**Reverse proxy path prefix** (`http.path_prefix`): when set (e.g. `/relay`), every server-generated redirect and every UI-rendered link, form action, and htmx attribute (`api/ui_templates_html/*.html`, `api/subscribe.go`) is prefixed via `Server.url()` / the `Prefix` field injected into `uiPageData`/`formPageData` by `renderUIPage`/`renderPage`. Route registration itself is unprefixed — the reverse proxy is expected to strip the prefix before forwarding requests to the backend. Must start with `/` and not end with `/`; empty (default) disables it.

**Event auto-expire sweep** (`notify/sweeper.go`): events left open (`end_time IS NULL`) past a configurable TTL are auto-closed by a background sweep, guarding against orphaned events — most commonly from `smtpapi`, which has no way to revisit an event after creation, but also from HTTP API callers that never call the end-event endpoint. Disabled by default (`event_sweep.enabled: false`); when enabled, auto-closed events are flagged via the `auto_closed` column/API field so callers can distinguish "the sweep stopped waiting" from an explicit resolution.

**SMTP ingestion** (`smtpapi/`): an inbound SMTP server that converts received email into notification relay jobs, so alerting tools that only know how to send email can target on-call groups. Each `RCPT TO` local part encodes a group and its delivery channels (`group+sms+voice`); Subject becomes the event name and the body the message. Auth is SASL PLAIN, LOGIN, or (opt-in) CRAM-MD5; PLAIN/LOGIN verify against LDAP, CRAM-MD5 verifies against a separate non-LDAP credential store (publisher/admin roles only either way). `smtp_server.tls_mode` selects `none` (plaintext), `starttls` (plaintext listener, upgraded via STARTTLS), or `tls` (implicit TLS, like SMTPS); `starttls`/`tls` require `tls_cert_file`/`tls_key_file`.

**SMTP CRAM-MD5 auth** (`smtpapi/cram*.go`, `api/cram_credentials.go`): CRAM-MD5 requires the server to hold the plaintext shared secret to compute HMAC-MD5 itself, which an LDAP bind never exposes — so it's backed by its own `smtp_cram_credentials` table instead of LDAP. Secrets are encrypted at rest with AES-256-GCM (`smtpapi.EncryptSecret`/`DecryptSecret`) under a server-held key (`smtp_server.cram_md5_secret_key`, base64-encoded 32 bytes) and decrypted only at auth time. Roles live directly on the credential row (no LDAP group indirection). Managed via admin-only `/api/v1/smtp/cram-credentials` endpoints and `nrcli smtp-cram add|list|remove`; `add` returns the generated secret exactly once.

**SMS self-service subscription** (`api/subscribe.go`): a small HTML form (no auth) where users can register/unregister their phone number for SMS alerts to a group, independent of LDAP group membership.

## Configuration (`config.yaml`)

All string values support `${ENV_VAR}` interpolation. Key sections:

```yaml
database:
  path: /path/to/db.sqlite
  max_reader_conns: 4         # default

http:
  listen_addr: ":8080"
  path_prefix: ""               # e.g. "/relay" when behind a reverse proxy at a subpath

ldap:
  primary_url: "ldaps://..."
  bind_dn: "CN=..."
  bind_password: "${LDAP_PASS}"
  user_base_dn: "OU=Users,DC=..."
  sync_groups: [grp-oncall]
  roles:
    admin: [grp-admins]
    publisher: [grp-oncall]
    reader: [grp-monitoring]

twilio:
  account_sid: "AC..."
  auth_token: "${TWILIO_TOKEN}"
  from_number: "+1..."

smtp:
  host: smtp.example.com
  port: 587
  username: user
  password: "${SMTP_PASS}"
  from_address: noreply@example.com
  tls_mode: starttls           # none | starttls | tls

smtp_server:                  # inbound SMTP ingestion; unset listen_addr disables it
  listen_addr: ":2525"
  domain: relay.local
  tls_mode: none                # none | starttls | tls; cert/key required for starttls and tls
  tls_cert_file: "/path/to/cert.pem"
  tls_key_file: "/path/to/key.pem"
  cram_md5_enabled: false       # opt-in CRAM-MD5 against a separate, non-LDAP credential store
  cram_md5_secret_key: "${SMTP_CRAM_MD5_KEY}"   # base64-encoded 32-byte AES-256 key; required if enabled

notify:
  worker_count: 4
  retry_limit: 3
  retry_delay: 60s
  delivery_timeout: 30s

event_sweep:
  enabled: false        # opt-in; auto-closes events left open past ttl
  ttl: 24h
  interval: 15m

severities: [none, information, warning, minor, major, critical]
```

## Implementation Status

Everything below is implemented and tested:
- Config loading with env var interpolation and validation
- SQLite connection pool (writer + reader)
- Database schema and migrations (Goose)
- LDAP authentication with LRU caching
- LDAP group membership sync
- HTTP server with Basic Auth middleware
- RBAC enforcement
- Audit logging
- Event and notification data model
- `POST /api/v1/notifications` — notification publishing endpoint
- Email delivery (SMTP, `notify/email.go`)
- SMS and voice delivery (Twilio, `notify/twilio.go`)
- Twilio status polling (`notify/poller.go`)
- Notification templating (`notify/template.go`, `api/templates.go`)
- SMTP ingestion server (`smtpapi/`)
- SMTP CRAM-MD5 auth against a separate non-LDAP credential store (`smtpapi/cram*.go`, `api/cram_credentials.go`)
- SMS self-service subscribe/unsubscribe form (`api/subscribe.go`)
- Event auto-expire sweep for orphaned events (`notify/sweeper.go`)
- CLI (`cmd/nrcli`)

There is no known stub/placeholder functionality remaining; treat this file as needing a re-check against the code whenever a major feature lands.

## Coding Conventions

- Provider interfaces in `notify/` — implement the interface, wire up in dispatcher
- New API endpoints: add route in `api/server.go`, handler in `api/`, apply auth middleware
- New DB queries: add SQL to `sql/`, run `sqlc generate`, use generated types
- Config additions: add to `config/config.go` struct with defaults set in the `defaults()` function
- Errors are returned, not panicked (except in `main.go` startup)
