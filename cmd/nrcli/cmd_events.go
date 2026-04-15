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
  get   EVENT_ID        Get an event by ID
  end   EVENT_ID        Mark an event as resolved
  notifications EVENT_ID  List notifications sent for an event

Flags for 'list':
  --limit N       Max records to return (1-200, default: 50)
  --offset N      Records to skip (default: 0)

Flags for 'create':
  --event-id ID         External event identifier (required)
  --name NAME           Event name
  --severity SEV        Severity (e.g. critical, major, minor, warning, information)
  --url URL             URL associated with the event
  --description DESC    Event description
  --start-time TIME     Start time in RFC3339 (default: now)

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
	case "end":
		runEventsEnd(cfg, args[1:])
	case "notifications":
		runEventsNotifications(cfg, args[1:])
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
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	q := url.Values{}
	q.Set("limit", strconv.Itoa(*limit))
	q.Set("offset", strconv.Itoa(*offset))

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

	if *eventID == "" {
		dief("--event-id is required")
	}

	req := map[string]interface{}{"event_id": *eventID}
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

func runEventsEnd(cfg *Config, args []string) {
	fs := newFlagSet("events end")
	endTime := fs.String("end-time", "", "end time (RFC3339; default: now)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("EVENT_ID argument is required")
	}
	eventID := fs.Arg(0)

	var req interface{}
	if *endTime != "" {
		req = map[string]interface{}{"end_time": *endTime}
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
