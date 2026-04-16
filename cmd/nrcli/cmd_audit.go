package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
)

const auditUsage = `Usage: nrcli audit [flags]

Lists the audit log. Requires admin role.

Flags:
  --username USER   Filter to entries for a specific user
  --from TIME       Lower bound on timestamp (RFC3339, inclusive)
  --to TIME         Upper bound on timestamp (RFC3339, inclusive)
  --limit N         Max records to return (1-200, default: 50)
  --offset N        Records to skip (default: 0)

Examples:
  nrcli audit --limit 20
  nrcli audit --username jdoe --from 2026-04-01T00:00:00Z
`

func runAudit(cfg *Config, args []string) {
	fs := newFlagSet("audit")
	username := fs.String("username", "", "filter by username")
	from := fs.String("from", "", "lower bound timestamp (RFC3339)")
	to := fs.String("to", "", "upper bound timestamp (RFC3339)")
	limit := fs.Int("limit", 50, "max records to return")
	offset := fs.Int("offset", 0, "records to skip")
	fs.Usage = func() { fmt.Fprint(os.Stderr, auditUsage) }
	parseFlags(fs, args)

	q := url.Values{}
	q.Set("limit", strconv.Itoa(*limit))
	q.Set("offset", strconv.Itoa(*offset))
	if *username != "" {
		q.Set("username", *username)
	}
	if *from != "" {
		q.Set("from", *from)
	}
	if *to != "" {
		q.Set("to", *to)
	}

	body, err := NewClient(cfg).Get("/api/v1/audit", q)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}

	var result struct {
		Entries []AuditLogEntry `json:"entries"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		die(err)
	}
	printAuditLogList(result.Entries)
}
