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
cmd/notification_relay/  # Entry point (main.go)
api/                     # HTTP server, routes, middleware, auth
config/                  # YAML config loading with env var interpolation
db/                      # SQLite connections, SQLc-generated queries
  migrations/            # Goose migration files
ldap/                    # LDAP client, authentication, group sync
notify/                  # Dispatcher, delivery workers, provider interfaces
sql/                     # Schema and SQLc query definitions
```

## Key Architecture Notes

**Startup sequence** (`cmd/notification_relay/main.go`):
1. Load config → open SQLite (writer + reader pool) → run migrations
2. Start goroutines: LDAP syncer, Twilio poller, notification dispatcher, HTTP server
3. Graceful shutdown on SIGINT/SIGTERM

**Database**: SQLite with WAL mode. Single writer connection (avoids `SQLITE_BUSY`), configurable reader pool (default 4). All queries are SQLc-generated — edit `sql/` then run `sqlc generate`.

**Authentication**: HTTP Basic Auth verified against LDAP, with an LRU cache (default 256 entries, 30s TTL). Group→Role mapping is configured in `config.yaml` under `ldap.roles`.

**RBAC roles**:
- `admin` — full access
- `publisher` — publish + read
- `reader` — read-only

**LDAP sync**: Periodic full delete+reinsert per configured group (default 15m). Membership stored in `group_members` table.

**Notification flow**: Event → Notification (targets groups) → expand to members → Delivery records per member per channel → dispatcher workers process queue.

**Twilio status**: Polled periodically (default 30s) as a webhook fallback due to firewall restrictions.

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

notify:
  worker_count: 4
  retry_limit: 3
  retry_delay: 60s
  delivery_timeout: 30s

severities: [none, information, warning, minor, major, critical]
```

## Implementation Status

**Production-ready:**
- Config loading with env var interpolation and validation
- SQLite connection pool (writer + reader)
- Database schema and migrations (Goose)
- LDAP authentication with LRU caching
- LDAP group membership sync
- HTTP server with Basic Auth middleware
- RBAC enforcement
- Audit logging
- Event and notification data model

**Stub/placeholder (needs implementation):**
- `POST /api/v1/notifications` — notification publishing endpoint
- Email delivery (`notify/` email provider)
- SMS delivery (`notify/` Twilio SMS provider)
- Voice delivery (`notify/` Twilio voice provider)
- Twilio status polling logic

## Coding Conventions

- Provider interfaces in `notify/` — implement the interface, wire up in dispatcher
- New API endpoints: add route in `api/server.go`, handler in `api/`, apply auth middleware
- New DB queries: add SQL to `sql/`, run `sqlc generate`, use generated types
- Config additions: add to `config/config.go` struct with defaults set in the `defaults()` function
- Errors are returned, not panicked (except in `main.go` startup)
