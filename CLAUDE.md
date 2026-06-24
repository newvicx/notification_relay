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
notify/                  # Dispatcher, delivery providers (email/SMS/voice), Twilio poller, templating
smtpapi/                 # SMTP ingestion server (email-to-notification gateway)
sql/                     # Schema and SQLc query definitions
```

## Key Architecture Notes

**Startup sequence** (`cmd/notification_relay/main.go`):
1. Load config → open SQLite (writer + reader pool) → run migrations
2. Start goroutines: LDAP syncer, Twilio poller, notification dispatcher, HTTP server, and (if `smtp_server.listen_addr` is set) the SMTP ingestion server
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

**SMTP ingestion** (`smtpapi/`): an inbound SMTP server that converts received email into notification relay jobs, so alerting tools that only know how to send email can target on-call groups. Each `RCPT TO` local part encodes a group and its delivery channels (`group+sms+voice`); Subject becomes the event name and the body the message. Auth is SASL PLAIN verified against LDAP (publisher/admin roles only). Supports implicit TLS (like SMTPS) when `smtp_server.tls_cert_file`/`tls_key_file` are set — STARTTLS is not supported.

**SMS self-service subscription** (`api/subscribe.go`): a small HTML form (no auth) where users can register/unregister their phone number for SMS alerts to a group, independent of LDAP group membership.

## Configuration (`config.yaml`)

All string values support `${ENV_VAR}` interpolation. Key sections:

```yaml
database:
  path: /path/to/db.sqlite
  max_reader_conns: 4         # default

http:
  listen_addr: ":8080"

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
  tls_cert_file: "/path/to/cert.pem"   # both set -> implicit TLS (no STARTTLS)
  tls_key_file: "/path/to/key.pem"

notify:
  worker_count: 4
  retry_limit: 3
  retry_delay: 60s
  delivery_timeout: 30s

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
- SMS self-service subscribe/unsubscribe form (`api/subscribe.go`)
- CLI (`cmd/nrcli`)

There is no known stub/placeholder functionality remaining; treat this file as needing a re-check against the code whenever a major feature lands.

## Coding Conventions

- Provider interfaces in `notify/` — implement the interface, wire up in dispatcher
- New API endpoints: add route in `api/server.go`, handler in `api/`, apply auth middleware
- New DB queries: add SQL to `sql/`, run `sqlc generate`, use generated types
- Config additions: add to `config/config.go` struct with defaults set in the `defaults()` function
- Errors are returned, not panicked (except in `main.go` startup)
