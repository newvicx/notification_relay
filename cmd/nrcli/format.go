package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ── API response types ────────────────────────────────────────────────────────

type Event struct {
	ID               int64  `json:"id"`
	EventID          string `json:"event_id"`
	EventURL         string `json:"event_url"`
	EventName        string `json:"event_name"`
	EventDescription string `json:"event_description"`
	EventSeverity    string `json:"event_severity"`
	StartTime        string `json:"start_time"`
	EndTime          string `json:"end_time"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	ModifiedBy       string `json:"modified_by"`
	ModifiedAt       string `json:"modified_at"`
}

type Notification struct {
	ID             int64         `json:"id"`
	NotificationID string        `json:"notification_id"`
	EventID        string        `json:"event_id"`
	Groups         []string      `json:"groups"`
	Destinations   []Destination `json:"destinations"`
	Channels       []string      `json:"channels"`
	Message        string        `json:"message"`
	MemberCount    int64         `json:"member_count"`
	Status         string        `json:"status"`
	ErrorMessage   string        `json:"error_message"`
	CreatedAt      string        `json:"created_at"`
	CreatedBy      string        `json:"created_by"`
}

// Destination is a direct notification target for a single channel.
type Destination struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
}

type Delivery struct {
	ID             int64          `json:"id"`
	DeliveryID     string         `json:"delivery_id"`
	NotificationID string         `json:"notification_id"`
	Group          string         `json:"group"`
	Member         string         `json:"member"`
	Destination    string         `json:"destination"`
	Channel        string         `json:"channel"`
	Status         string         `json:"status"`
	EmailTemplate  string         `json:"email_template"`
	EmailVars      map[string]any `json:"email_vars"`
	Attempt        int64          `json:"attempt"`
	PollAttempts   int64          `json:"poll_attempts"`
	ErrorMessage   string         `json:"error_message"`
	SentAt         string         `json:"sent_at"`
	CompletedAt    string         `json:"completed_at"`
	TwilioSID      string         `json:"twilio_sid"`
}

type GroupMember struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Mobile      string `json:"mobile"`
	Work        string `json:"work"`
	SyncedAt    string `json:"synced_at"`
}

type Template struct {
	ID           int64    `json:"id"`
	TemplateName string   `json:"template_name"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	RequiredVars []string `json:"required_vars"`
	Description  string   `json:"description"`
}

type AuditLogEntry struct {
	ID            int64  `json:"id"`
	Timestamp     string `json:"timestamp"`
	Username      string `json:"username"`
	IPAddress     string `json:"ip_address"`
	Action        string `json:"action"`
	ImpactedTable string `json:"impacted_table"`
	OldValues     string `json:"old_values"`
	NewValues     string `json:"new_values"`
}

type PublishResponse struct {
	NotificationID string        `json:"notification_id"`
	EventID        string        `json:"event_id"`
	Groups         []string      `json:"groups"`
	Destinations   []Destination `json:"destinations"`
	Channels       []string      `json:"channels"`
	Message        string        `json:"message"`
	Status         string        `json:"status"`
}

type SyncGroup struct {
	ID        int64  `json:"id"`
	GroupName string `json:"group_name"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

// EventSummary is the joined structure returned by `events summary`.
type EventSummary struct {
	Event         Event                        `json:"event"`
	Notifications []NotificationWithDeliveries `json:"notifications"`
}

type NotificationWithDeliveries struct {
	Notification Notification `json:"notification"`
	Deliveries   []Delivery   `json:"deliveries"`
}

// ── Flag helpers ──────────────────────────────────────────────────────────────

// stringSlice is a flag.Value that can be specified multiple times.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// kvMap is a flag.Value for repeated --key=VALUE pairs.
type kvMap map[string]string

func (m *kvMap) String() string {
	if *m == nil {
		return ""
	}
	parts := make([]string, 0, len(*m))
	for k, v := range *m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}
func (m *kvMap) Set(v string) error {
	if *m == nil {
		*m = make(kvMap)
	}
	idx := strings.IndexByte(v, '=')
	if idx < 0 {
		return fmt.Errorf("expected KEY=VALUE, got %q", v)
	}
	(*m)[v[:idx]] = v[idx+1:]
	return nil
}

// destinationSlice is a flag.Value for repeated --destination CHANNEL:TARGET flags.
type destinationSlice []Destination

func (d *destinationSlice) String() string {
	parts := make([]string, len(*d))
	for i, dest := range *d {
		parts[i] = dest.Channel + ":" + dest.Target
	}
	return strings.Join(parts, ",")
}

func (d *destinationSlice) Set(v string) error {
	idx := strings.IndexByte(v, ':')
	if idx < 0 {
		return fmt.Errorf("expected CHANNEL:TARGET, got %q", v)
	}
	*d = append(*d, Destination{Channel: v[:idx], Target: v[idx+1:]})
	return nil
}

// newFlagSet creates a FlagSet that exits cleanly on --help.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// parseFlags parses args into fs, exiting on error or --help.
func parseFlags(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}
}

// ── Error helpers ─────────────────────────────────────────────────────────────

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// ── String helpers ────────────────────────────────────────────────────────────

func strOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ptrOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// formatDestinations renders a []Destination as "channel:target, ..." or "" if empty.
func formatDestinations(dests []Destination) string {
	if len(dests) == 0 {
		return ""
	}
	parts := make([]string, len(dests))
	for i, d := range dests {
		parts[i] = d.Channel + ":" + d.Target
	}
	return strings.Join(parts, ", ")
}

// deliveryRecipient returns the member username for group deliveries or the
// destination target for direct deliveries.
func deliveryRecipient(d Delivery) string {
	if d.Member != "" {
		return d.Member
	}
	return strOrDash(d.Destination)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// shortTime formats an RFC3339 timestamp as "YYYY-MM-DD HH:MM:SS".
func shortTime(s string) string {
	if s == "" {
		return "-"
	}
	if len(s) >= 19 {
		return strings.ReplaceAll(s[:19], "T", " ")
	}
	return s
}

// ── JSON output ───────────────────────────────────────────────────────────────

func printJSON(data []byte) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		fmt.Println(string(data))
		return
	}
	fmt.Println(buf.String())
}

// ── Table helpers ─────────────────────────────────────────────────────────────

func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// ── Events ────────────────────────────────────────────────────────────────────

func printEventList(events []Event) {
	if len(events) == 0 {
		fmt.Println("No events found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "EVENT ID\tSEVERITY\tNAME\tSTART TIME\tEND TIME")
	for _, e := range events {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			e.EventID,
			strOrDash(e.EventSeverity),
			strOrDash(truncate(e.EventName, 40)),
			shortTime(e.StartTime),
			strOrDash(e.EndTime),
		)
	}
	w.Flush()
}

func printEventDetail(e Event) {
	w := newTabWriter()
	fmt.Fprintf(w, "ID:\t%d\n", e.ID)
	fmt.Fprintf(w, "Event ID:\t%s\n", e.EventID)
	fmt.Fprintf(w, "Name:\t%s\n", strOrDash(e.EventName))
	fmt.Fprintf(w, "Severity:\t%s\n", strOrDash(e.EventSeverity))
	fmt.Fprintf(w, "URL:\t%s\n", strOrDash(e.EventURL))
	fmt.Fprintf(w, "Description:\t%s\n", strOrDash(e.EventDescription))
	fmt.Fprintf(w, "Start Time:\t%s\n", e.StartTime)
	fmt.Fprintf(w, "End Time:\t%s\n", strOrDash(e.EndTime))
	fmt.Fprintf(w, "Created By:\t%s\n", strOrDash(e.CreatedBy))
	fmt.Fprintf(w, "Created At:\t%s\n", strOrDash(shortTime(e.CreatedAt)))
	fmt.Fprintf(w, "Modified By:\t%s\n", strOrDash(e.ModifiedBy))
	fmt.Fprintf(w, "Modified At:\t%s\n", strOrDash(shortTime(e.ModifiedAt)))
	w.Flush()
}

// ── Notifications ─────────────────────────────────────────────────────────────

func printNotificationList(notifs []Notification) {
	if len(notifs) == 0 {
		fmt.Println("No notifications found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "NOTIFICATION ID\tEVENT ID\tGROUPS\tDESTINATIONS\tCHANNELS\tMEMBERS\tSTATUS\tCREATED AT")
	for _, n := range notifs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			n.NotificationID,
			n.EventID,
			strOrDash(strings.Join(n.Groups, ",")),
			strOrDash(formatDestinations(n.Destinations)),
			strOrDash(strings.Join(n.Channels, ",")),
			n.MemberCount,
			n.Status,
			shortTime(n.CreatedAt),
		)
	}
	w.Flush()
}

func printNotificationDetail(n Notification) {
	w := newTabWriter()
	fmt.Fprintf(w, "ID:\t%d\n", n.ID)
	fmt.Fprintf(w, "Notification ID:\t%s\n", n.NotificationID)
	fmt.Fprintf(w, "Event ID:\t%s\n", n.EventID)
	fmt.Fprintf(w, "Groups:\t%s\n", strOrDash(strings.Join(n.Groups, ", ")))
	fmt.Fprintf(w, "Destinations:\t%s\n", strOrDash(formatDestinations(n.Destinations)))
	fmt.Fprintf(w, "Channels:\t%s\n", strOrDash(strings.Join(n.Channels, ", ")))
	fmt.Fprintf(w, "Message:\t%s\n", n.Message)
	fmt.Fprintf(w, "Member Count:\t%d\n", n.MemberCount)
	fmt.Fprintf(w, "Status:\t%s\n", n.Status)
	fmt.Fprintf(w, "Error Message:\t%s\n", strOrDash(n.ErrorMessage))
	fmt.Fprintf(w, "Created At:\t%s\n", n.CreatedAt)
	fmt.Fprintf(w, "Created By:\t%s\n", strOrDash(n.CreatedBy))
	w.Flush()
}

func printPublishResponse(r PublishResponse) {
	w := newTabWriter()
	fmt.Fprintf(w, "Notification ID:\t%s\n", r.NotificationID)
	fmt.Fprintf(w, "Event ID:\t%s\n", r.EventID)
	fmt.Fprintf(w, "Groups:\t%s\n", strOrDash(strings.Join(r.Groups, ", ")))
	fmt.Fprintf(w, "Destinations:\t%s\n", strOrDash(formatDestinations(r.Destinations)))
	fmt.Fprintf(w, "Channels:\t%s\n", strOrDash(strings.Join(r.Channels, ", ")))
	fmt.Fprintf(w, "Message:\t%s\n", r.Message)
	fmt.Fprintf(w, "Status:\t%s\n", r.Status)
	w.Flush()
}

// ── Deliveries ────────────────────────────────────────────────────────────────

func printDeliveryList(deliveries []Delivery) {
	if len(deliveries) == 0 {
		fmt.Println("No deliveries found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "DELIVERY ID\tRECIPIENT\tCHANNEL\tSTATUS\tATTEMPT\tSENT AT\tERROR")
	for _, d := range deliveries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			d.DeliveryID,
			deliveryRecipient(d),
			d.Channel,
			d.Status,
			d.Attempt,
			shortTime(d.SentAt),
			strOrDash(d.ErrorMessage),
		)
	}
	w.Flush()
}

func printDeliveryDetail(d Delivery) {
	emailVars, err := json.Marshal(d.EmailVars)
	if err != nil {
		die(err)
	}
	w := newTabWriter()
	fmt.Fprintf(w, "ID:\t%d\n", d.ID)
	fmt.Fprintf(w, "Delivery ID:\t%s\n", d.DeliveryID)
	fmt.Fprintf(w, "Notification ID:\t%s\n", d.NotificationID)
	fmt.Fprintf(w, "Group:\t%s\n", strOrDash(d.Group))
	fmt.Fprintf(w, "Member:\t%s\n", strOrDash(d.Member))
	fmt.Fprintf(w, "Destination:\t%s\n", strOrDash(d.Destination))
	fmt.Fprintf(w, "Channel:\t%s\n", d.Channel)
	fmt.Fprintf(w, "Status:\t%s\n", d.Status)
	fmt.Fprintf(w, "Attempt:\t%d\n", d.Attempt)
	fmt.Fprintf(w, "Poll Attempts:\t%d\n", d.PollAttempts)
	fmt.Fprintf(w, "Email Template:\t%s\n", strOrDash(d.EmailTemplate))
	fmt.Fprintf(w, "Email Vars:\t%s\n", strOrDash(string(emailVars)))
	fmt.Fprintf(w, "Error:\t%s\n", strOrDash(d.ErrorMessage))
	fmt.Fprintf(w, "Sent At:\t%s\n", d.SentAt)
	fmt.Fprintf(w, "Completed At:\t%s\n", strOrDash(d.CompletedAt))
	w.Flush()
}

// ── Groups ────────────────────────────────────────────────────────────────────

func printGroupList(groups []string) {
	if len(groups) == 0 {
		fmt.Println("No groups found.")
		return
	}
	for _, g := range groups {
		fmt.Println(g)
	}
}

func printGroupMemberList(members []GroupMember) {
	if len(members) == 0 {
		fmt.Println("No members found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "USERNAME\tDISPLAY NAME\tEMAIL\tMOBILE\tSYNCED AT")
	for _, m := range members {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			m.Username,
			strOrDash(m.DisplayName),
			strOrDash(m.Email),
			strOrDash(m.Mobile),
			shortTime(m.SyncedAt),
		)
	}
	w.Flush()
}

// ── Templates ─────────────────────────────────────────────────────────────────

func printTemplateList(templates []Template) {
	if len(templates) == 0 {
		fmt.Println("No templates found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "NAME\tSUBJECT\tREQUIRED VARS\tDESCRIPTION")
	for _, t := range templates {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			t.TemplateName,
			truncate(t.Subject, 50),
			strings.Join(t.RequiredVars, ","),
			strOrDash(truncate(t.Description, 40)),
		)
	}
	w.Flush()
}

func printTemplateDetail(t Template) {
	w := newTabWriter()
	fmt.Fprintf(w, "ID:\t%d\n", t.ID)
	fmt.Fprintf(w, "Name:\t%s\n", t.TemplateName)
	fmt.Fprintf(w, "Subject:\t%s\n", t.Subject)
	fmt.Fprintf(w, "Required Vars:\t%s\n", strings.Join(t.RequiredVars, ", "))
	fmt.Fprintf(w, "Description:\t%s\n", strOrDash(t.Description))
	w.Flush()
	fmt.Println()
	fmt.Println("--- Body ---")
	fmt.Println(t.Body)
}

// ── Audit log ─────────────────────────────────────────────────────────────────

func printAuditLogList(entries []AuditLogEntry) {
	if len(entries) == 0 {
		fmt.Println("No audit log entries found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "TIMESTAMP\tUSERNAME\tACTION\tTABLE\tIP")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			shortTime(e.Timestamp),
			e.Username,
			e.Action,
			e.ImpactedTable,
			strOrDash(e.IPAddress),
		)
	}
	w.Flush()
}

// ── Sync groups ───────────────────────────────────────────────────────────────

func printSyncGroupList(groups []SyncGroup) {
	if len(groups) == 0 {
		fmt.Println("No sync groups configured.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "GROUP NAME\tCREATED BY\tCREATED AT")
	for _, g := range groups {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			g.GroupName,
			g.CreatedBy,
			shortTime(g.CreatedAt),
		)
	}
	w.Flush()
}

func printSyncGroupDetail(g SyncGroup) {
	w := newTabWriter()
	fmt.Fprintf(w, "ID:\t%d\n", g.ID)
	fmt.Fprintf(w, "Group Name:\t%s\n", g.GroupName)
	fmt.Fprintf(w, "Created By:\t%s\n", g.CreatedBy)
	fmt.Fprintf(w, "Created At:\t%s\n", g.CreatedAt)
	w.Flush()
}

// ── SMS Subscriptions ─────────────────────────────────────────────────────────

// SMSSubscription mirrors api.smsSubscriptionResponse.
type SMSSubscription struct {
	Username     string `json:"username"`
	Phone        string `json:"phone"`
	SubscribedAt string `json:"subscribed_at"`
}

func printSMSSubscriptionList(subs []SMSSubscription) {
	if len(subs) == 0 {
		fmt.Println("No SMS subscriptions found.")
		return
	}
	w := newTabWriter()
	fmt.Fprintln(w, "USERNAME\tPHONE\tSUBSCRIBED AT")
	for _, s := range subs {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			s.Username,
			s.Phone,
			shortTime(s.SubscribedAt),
		)
	}
	w.Flush()
}

func printSMSSubscriptionDetail(s SMSSubscription) {
	w := newTabWriter()
	fmt.Fprintf(w, "Username:\t%s\n", s.Username)
	fmt.Fprintf(w, "Phone:\t%s\n", s.Phone)
	fmt.Fprintf(w, "Subscribed At:\t%s\n", shortTime(s.SubscribedAt))
	w.Flush()
}

// ── Event summary ─────────────────────────────────────────────────────────────

// printEventSummary renders the full joined event → notifications → deliveries
// hierarchy in a human-readable, indented format.
func printEventSummary(s EventSummary) {
	e := s.Event

	// ── Event header ──────────────────────────────────────────────────────────
	status := "active"
	if e.EndTime != "" {
		status = "resolved"
	}
	severity := strOrDash(e.EventSeverity)
	fmt.Printf("Event: %s  [%s]  %s\n", e.EventID, strings.ToUpper(severity), status)

	if e.EventName != "" {
		fmt.Printf("  Name:        %s\n", e.EventName)
	}
	if e.EventDescription != "" {
		fmt.Printf("  Description: %s\n", e.EventDescription)
	}
	if e.EventURL != "" {
		fmt.Printf("  URL:         %s\n", e.EventURL)
	}
	fmt.Printf("  Started:     %s\n", shortTime(e.StartTime))
	if e.EndTime != "" {
		fmt.Printf("  Ended:       %s\n", shortTime(e.EndTime))
	}

	if len(s.Notifications) == 0 {
		fmt.Println("\n  No notifications.")
		return
	}

	// ── Notifications ─────────────────────────────────────────────────────────
	for i, nd := range s.Notifications {
		n := nd.Notification
		fmt.Printf("\n  Notification %d/%d  [%s]  %s\n",
			i+1, len(s.Notifications),
			n.NotificationID,
			shortTime(n.CreatedAt),
		)
		fmt.Printf("    Groups:       %s\n", strOrDash(strings.Join(n.Groups, ", ")))
		fmt.Printf("    Destinations: %s\n", strOrDash(formatDestinations(n.Destinations)))
		fmt.Printf("    Channels:     %s\n", strOrDash(strings.Join(n.Channels, ", ")))
		fmt.Printf("    Members:      %d\n", n.MemberCount)
		fmt.Printf("    Status:       %s\n", n.Status)
		if n.ErrorMessage != "" {
			fmt.Printf("    Error:        %s\n", n.ErrorMessage)
		}
		fmt.Printf("    Message:      %s\n", n.Message)

		if len(nd.Deliveries) == 0 {
			fmt.Println("    No deliveries.")
			continue
		}

		// ── Deliveries table ──────────────────────────────────────────────────
		fmt.Printf("\n    Deliveries (%d):\n", len(nd.Deliveries))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "    DELIVERY ID\tRECIPIENT\tGROUP\tCHANNEL\tSTATUS\tATTEMPT\tSENT AT\tERROR")
		for _, d := range nd.Deliveries {
			fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				d.DeliveryID,
				deliveryRecipient(d),
				strOrDash(d.Group),
				d.Channel,
				d.Status,
				d.Attempt,
				shortTime(d.SentAt),
				strOrDash(d.ErrorMessage),
			)
		}
		w.Flush()
	}
}
