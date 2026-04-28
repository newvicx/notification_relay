package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const notificationsUsage = `Usage: nrcli notifications <subcommand> [flags] [arguments]

Subcommands:
  publish               Queue a notification for delivery
  get   NOTIFICATION_ID  Get a notification by ID
  deliveries NOTIFICATION_ID  List delivery attempts for a notification

Flags for 'publish':
  --message MSG           Notification message (required)
  --group GROUP           LDAP group to notify; repeat for multiple
  --channel CHANNEL       Delivery channel for groups: sms, voice, email; repeat for multiple
                          (required when --group is specified)
  --destination CH:TARGET Direct delivery target as CHANNEL:TARGET; repeat for multiple
                          e.g. --destination sms:+12125551234
                               --destination email:ops@example.com
  --event-id ID           External event identifier
  --event-name NAME       Event name (used when auto-creating the event)
  --event-severity SEV    Event severity
  --event-url URL         Event URL
  --event-description D   Event description
  --start-time TIME       Event start time (RFC3339; default: now)
  --end-time TIME         Event end time (RFC3339); marks the event as ended
  --email-template TMPL   Email template name (required when any channel is email)
  --email-vars JSON       Template variables defined as a JSON string ("{\"test\": 1}")

At least one --group or --destination must be provided.

Examples:
  # Notify an LDAP group
  nrcli notifications publish \
    --event-id alert-disk-01 \
    --group grp-oncall \
    --channel sms --channel email \
    --message "Disk usage above 90% on web-01" \
    --email-template alert-standard \
    --email-vars "{\"host\": \"web-01\"}"

  # Notify a direct phone number and email address
  nrcli notifications publish \
    --event-id alert-disk-01 \
    --destination sms:+12125551234 \
    --destination email:ops@example.com \
    --message "Disk usage above 90% on web-01" \
    --email-template alert-standard
`

func runNotifications(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, notificationsUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "publish":
		runNotificationsPublish(cfg, args[1:])
	case "get":
		runNotificationsGet(cfg, args[1:])
	case "deliveries":
		runNotificationsDeliveries(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown notifications subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, notificationsUsage)
		os.Exit(1)
	}
}

func runNotificationsPublish(cfg *Config, args []string) {
	fs := newFlagSet("notifications publish")

	eventID := fs.String("event-id", "", "external event identifier (required)")
	message := fs.String("message", "", "notification message (required)")
	eventName := fs.String("event-name", "", "event name")
	eventSeverity := fs.String("event-severity", "", "event severity")
	eventURL := fs.String("event-url", "", "event URL")
	eventDesc := fs.String("event-description", "", "event description")
	startTime := fs.String("start-time", "", "event start time (RFC3339)")
	endTime := fs.String("end-time", "", "event end time (RFC3339); marks the event as ended")
	emailTemplate := fs.String("email-template", "", "email template name (required when channel includes email)")
	emailVars := fs.String("email-vars", "", "template variables as a JSON string")

	var groups stringSlice
	var channels stringSlice
	var destinations destinationSlice
	fs.Var(&groups, "group", "LDAP group to notify (repeat for multiple)")
	fs.Var(&channels, "channel", "delivery channel for groups: sms, voice, email (repeat for multiple)")
	fs.Var(&destinations, "destination", "direct delivery target as CHANNEL:TARGET (repeat for multiple)")

	fs.Usage = func() { fmt.Fprint(os.Stderr, notificationsUsage) }
	parseFlags(fs, args)

	if len(groups) == 0 && len(destinations) == 0 {
		dief("at least one --group or --destination is required")
	}
	if len(groups) > 0 && len(channels) == 0 {
		dief("--channel is required when --group is specified")
	}
	if *message == "" {
		dief("--message is required")
	}

	req := map[string]any{
		"message": *message,
	}
	if len(groups) > 0 {
		req["groups"] = []string(groups)
	}
	if len(channels) > 0 {
		req["channels"] = []string(channels)
	}
	if len(destinations) > 0 {
		req["destinations"] = []Destination(destinations)
	}
	if *eventID != "" {
		req["event_id"] = *eventID
	}
	if *eventName != "" {
		req["event_name"] = *eventName
	}
	if *eventSeverity != "" {
		req["event_severity"] = *eventSeverity
	}
	if *eventURL != "" {
		req["event_url"] = *eventURL
	}
	if *eventDesc != "" {
		req["event_description"] = *eventDesc
	}
	if *startTime != "" {
		req["start_time"] = *startTime
	}
	if *endTime != "" {
		req["end_time"] = *endTime
	}
	if *emailTemplate != "" {
		req["email_template"] = *emailTemplate
	}
	if *emailVars != "" {
		var unMarshaled map[string]any
		if err := json.Unmarshal([]byte(*emailVars), &unMarshaled); err != nil {
			die(err)
		}
		req["email_vars"] = unMarshaled
	}

	_, body, err := NewClient(cfg).Post("/api/v1/notifications", req)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var resp PublishResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		die(err)
	}
	printPublishResponse(resp)
}

func runNotificationsGet(cfg *Config, args []string) {
	fs := newFlagSet("notifications get")
	fs.Usage = func() { fmt.Fprint(os.Stderr, notificationsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("NOTIFICATION_ID argument is required")
	}
	notifID := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/notifications/"+url.PathEscape(notifID), nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var n Notification
	if err := json.Unmarshal(body, &n); err != nil {
		die(err)
	}
	printNotificationDetail(n)
}

func runNotificationsDeliveries(cfg *Config, args []string) {
	fs := newFlagSet("notifications deliveries")
	fs.Usage = func() { fmt.Fprint(os.Stderr, notificationsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("NOTIFICATION_ID argument is required")
	}
	notifID := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/notifications/"+url.PathEscape(notifID)+"/deliveries", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var deliveries []Delivery
	if err := json.Unmarshal(body, &deliveries); err != nil {
		die(err)
	}
	printDeliveryList(deliveries)
}
