package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
)

const eventsUsage = `Usage: nrcli events <subcommand> [flags] [arguments]

Subcommands:
  list                  List events (newest first)
  create                Create a new event
  get    EVENT_ID       Get an event by ID
  update EVENT_ID       Update mutable fields on an event
  end    EVENT_ID       Mark an event as resolved
  notifications EVENT_ID  List notifications sent for an event
  summary   EVENT_ID    Show full event detail with all notifications and deliveries

Flags for 'list':
  --limit N           Max records to return (1-200, default: 50)
  --offset N          Records to skip (default: 0)
  --start-from TIME   Lower bound on start_time (RFC3339, inclusive)
  --start-to TIME     Upper bound on start_time (RFC3339, inclusive)

Flags for 'create':
  --event-id ID         External event identifier
  --name NAME           Event name
  --severity SEV        Severity (e.g. critical, major, minor, warning, information)
  --url URL             URL associated with the event
  --description DESC    Event description
  --start-time TIME     Start time in RFC3339 (default: now)

Flags for 'update':
  --name NAME           New event name
  --severity SEV        New severity
  --url URL             New URL
  --description DESC    New description

Flags for 'end':
  --end-time TIME       End time in RFC3339 (default: now)
`

func runEvents(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, eventsUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runEventsList(cfg, args[1:])
	case "create":
		runEventsCreate(cfg, args[1:])
	case "get":
		runEventsGet(cfg, args[1:])
	case "update":
		runEventsUpdate(cfg, args[1:])
	case "end":
		runEventsEnd(cfg, args[1:])
	case "notifications":
		runEventsNotifications(cfg, args[1:])
	case "summary":
		runEventsSummary(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown events subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, eventsUsage)
		os.Exit(1)
	}
}

func runEventsList(cfg *Config, args []string) {
	fs := newFlagSet("events list")
	limit := fs.Int("limit", 50, "max records to return")
	offset := fs.Int("offset", 0, "records to skip")
	startFrom := fs.String("start-from", "", "lower bound on start_time (RFC3339, inclusive)")
	startTo := fs.String("start-to", "", "upper bound on start_time (RFC3339, inclusive)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	q := url.Values{}
	q.Set("limit", strconv.Itoa(*limit))
	q.Set("offset", strconv.Itoa(*offset))
	if *startFrom != "" {
		q.Set("start_from", *startFrom)
	}
	if *startTo != "" {
		q.Set("start_to", *startTo)
	}

	body, err := NewClient(cfg).Get("/api/v1/events", q)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}

	var result struct {
		Events []Event `json:"events"`
		Limit  int     `json:"limit"`
		Offset int     `json:"offset"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		die(err)
	}
	printEventList(result.Events)
}

func runEventsCreate(cfg *Config, args []string) {
	fs := newFlagSet("events create")
	eventID := fs.String("event-id", "", "external event identifier (required)")
	name := fs.String("name", "", "event name")
	severity := fs.String("severity", "", "severity level")
	eventURL := fs.String("url", "", "URL associated with the event")
	desc := fs.String("description", "", "event description")
	startTime := fs.String("start-time", "", "start time (RFC3339; default: now)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	req := map[string]any{}
	if *eventID != "" {
		req["event_id"] = *eventID
	}
	if *name != "" {
		req["event_name"] = *name
	}
	if *severity != "" {
		req["event_severity"] = *severity
	}
	if *eventURL != "" {
		req["event_url"] = *eventURL
	}
	if *desc != "" {
		req["event_description"] = *desc
	}
	if *startTime != "" {
		req["start_time"] = *startTime
	}

	status, body, err := NewClient(cfg).Post("/api/v1/events", req)
	if err != nil {
		die(err)
	}
	_ = status

	if cfg.JSON {
		printJSON(body)
		return
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		die(err)
	}
	printEventDetail(e)
}

func runEventsGet(cfg *Config, args []string) {
	fs := newFlagSet("events get")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/events/"+url.PathEscape(eventID), nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		die(err)
	}
	printEventDetail(e)
}

func runEventsUpdate(cfg *Config, args []string) {
	fs := newFlagSet("events update")
	name := fs.String("name", "", "new event name")
	severity := fs.String("severity", "", "new severity level")
	eventURL := fs.String("url", "", "new URL associated with the event")
	desc := fs.String("description", "", "new event description")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)

	req := map[string]any{}
	if *name != "" {
		req["event_name"] = *name
	}
	if *severity != "" {
		req["event_severity"] = *severity
	}
	if *eventURL != "" {
		req["event_url"] = *eventURL
	}
	if *desc != "" {
		req["event_description"] = *desc
	}
	if len(req) == 0 {
		dief("at least one of --name, --severity, --url, or --description is required")
	}

	_, body, err := NewClient(cfg).Patch("/api/v1/events/"+url.PathEscape(eventID), req)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		die(err)
	}
	printEventDetail(e)
}

func runEventsEnd(cfg *Config, args []string) {
	fs := newFlagSet("events end")
	endTime := fs.String("end-time", "", "end time (RFC3339; default: now)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)

	var req any
	if *endTime != "" {
		req = map[string]any{"end_time": *endTime}
	}

	_, body, err := NewClient(cfg).Post("/api/v1/events/"+url.PathEscape(eventID)+"/end", req)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		die(err)
	}
	printEventDetail(e)
}

func runEventsNotifications(cfg *Config, args []string) {
	fs := newFlagSet("events notifications")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/events/"+url.PathEscape(eventID)+"/notifications", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var notifs []Notification
	if err := json.Unmarshal(body, &notifs); err != nil {
		die(err)
	}
	printNotificationList(notifs)
}

// runEventsSummary fetches an event, all its notifications, and all deliveries
// for each notification, then renders them as a single joined structure.
func runEventsSummary(cfg *Config, args []string) {
	fs := newFlagSet("events summary")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)
	client := NewClient(cfg)

	// Fetch event.
	eventBody, err := client.Get("/api/v1/events/"+url.PathEscape(eventID), nil)
	if err != nil {
		die(err)
	}
	var event Event
	if err := json.Unmarshal(eventBody, &event); err != nil {
		die(err)
	}

	// Fetch notifications for the event.
	notifsBody, err := client.Get("/api/v1/events/"+url.PathEscape(eventID)+"/notifications", nil)
	if err != nil {
		die(err)
	}
	var notifs []Notification
	if err := json.Unmarshal(notifsBody, &notifs); err != nil {
		die(err)
	}

	// Fetch deliveries for each notification.
	summary := EventSummary{Event: event, Notifications: make([]NotificationWithDeliveries, len(notifs))}
	for i, n := range notifs {
		deliveriesBody, err := client.Get("/api/v1/notifications/"+url.PathEscape(n.NotificationID)+"/deliveries", nil)
		if err != nil {
			die(err)
		}
		var deliveries []Delivery
		if err := json.Unmarshal(deliveriesBody, &deliveries); err != nil {
			die(err)
		}
		summary.Notifications[i] = NotificationWithDeliveries{
			Notification: n,
			Deliveries:   deliveries,
		}
	}

	if cfg.JSON {
		data, err := json.Marshal(summary)
		if err != nil {
			die(err)
		}
		printJSON(data)
		return
	}
	printEventSummary(summary)
}
