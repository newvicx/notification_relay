package main

import (
	"flag"
	"fmt"
	"os"
)

const globalUsage = `nrcli - Notification Relay CLI

Usage:
  nrcli [global flags] <command> [subcommand] [flags] [arguments]

Global Flags:
  --url URL          API base URL [$NR_URL, default: http://localhost:8080]
  --user USER        Basic Auth username [$NR_USER]
  --password PASS    Basic Auth password [$NR_PASSWORD]
  --json             Output raw JSON instead of formatted tables

Commands:
  events             Manage events
  notifications      Publish and inspect notifications
  deliveries         Inspect delivery attempts
  groups             View LDAP group membership and manage sync configuration
  templates          Manage email templates
  audit              View audit log (admin only)
  subscriptions      Manage SMS subscriptions

Run 'nrcli <command>' for subcommand help.

Examples:
  nrcli --user admin --password secret events list
  nrcli events get alert-disk-01
  nrcli notifications publish --event-id alert-disk-01 --group grp-oncall --channel sms --message "Disk full"
  nrcli groups sync list
  nrcli groups sync add grp-oncall
  nrcli templates list --json
`

// Config holds global connection settings passed to all commands.
type Config struct {
	URL      string
	Username string
	Password string
	JSON     bool
}

func main() {
	fs := flag.NewFlagSet("nrcli", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, globalUsage) }

	urlFlag := fs.String("url", envOr("NR_URL", "http://localhost:8080"), "API base URL")
	userFlag := fs.String("user", envOr("NR_USER", ""), "Basic Auth username")
	passFlag := fs.String("password", envOr("NR_PASSWORD", ""), "Basic Auth password")
	jsonFlag := fs.Bool("json", false, "Output raw JSON")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	cfg := &Config{
		URL:      *urlFlag,
		Username: *userFlag,
		Password: *passFlag,
		JSON:     *jsonFlag,
	}

	switch args[0] {
	case "events":
		runEvents(cfg, args[1:])
	case "notifications":
		runNotifications(cfg, args[1:])
	case "deliveries":
		runDeliveries(cfg, args[1:])
	case "groups":
		runGroups(cfg, args[1:])
	case "templates":
		runTemplates(cfg, args[1:])
	case "audit":
		runAudit(cfg, args[1:])
	case "subscriptions":
		runSubscriptions(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", args[0])
		fs.Usage()
		os.Exit(1)
	}
}

// envOr returns the value of the environment variable key, or def if not set.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
