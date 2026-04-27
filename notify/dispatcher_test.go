package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"notification_relay/config"
	"notification_relay/db"
	"notification_relay/testutil"
)

// ---- Stub providers ----

type emailCall struct{ to, subject, body string }

type stubEmail struct {
	mu    sync.Mutex
	calls []emailCall
	err   error
}

func (s *stubEmail) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	s.calls = append(s.calls, emailCall{to, subject, body})
	s.mu.Unlock()
	return s.err
}

type smsCall struct{ to, message string }

type stubSMS struct {
	mu     sync.Mutex
	calls  []smsCall
	sid    string
	status string
	err    error
}

func (s *stubSMS) Send(to, message string) (string, string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, smsCall{to, message})
	s.mu.Unlock()
	if s.err != nil {
		return "", "", s.err
	}
	return s.sid, s.status, nil
}

type voiceCall struct{ to, message string }

type stubVoice struct {
	mu     sync.Mutex
	calls  []voiceCall
	sid    string
	status string
	err    error
}

func (v *stubVoice) Call(to, message string) (string, string, error) {
	v.mu.Lock()
	v.calls = append(v.calls, voiceCall{to, message})
	v.mu.Unlock()
	if v.err != nil {
		return "", "", v.err
	}
	return v.sid, v.status, nil
}

// ---- Helpers ----

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func defaultCfg() config.NotifyConfig {
	return config.NotifyConfig{
		WorkerCount:         1,
		RetryLimit:          1,
		RetryDelay:          0,
		DeliveryTimeout:     5 * time.Second,
		DeliveryConcurrency: 4,
	}
}

// funcSMS is an SMSProvider backed by an arbitrary function, useful for
// injecting custom behaviour (e.g. tracking concurrency) in tests.
type funcSMS struct {
	sendFn func(to, message string) (string, string, error)
}

func (f *funcSMS) Send(to, message string) (string, string, error) {
	return f.sendFn(to, message)
}

func insertNotification(t *testing.T, q *db.Queries, eventID string, groups, channels []string, message string) db.Notification {
	t.Helper()
	ctx := context.Background()

	groupsJSON, _ := json.Marshal(groups)
	channelsJSON, _ := json.Marshal(channels)

	// Ensure event exists.
	if _, err := q.GetEventByEventID(ctx, eventID); errors.Is(err, sql.ErrNoRows) {
		q.InsertEvent(ctx, db.InsertEventParams{
			EventID:   eventID,
			StartTime: time.Now().UTC().Format(time.RFC3339),
		})
	}

	n, err := q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: uuidV7(),
		EventID:        eventID,
		Groups:         sql.NullString{String: string(groupsJSON), Valid: true},
		Channels:       sql.NullString{String: string(channelsJSON), Valid: len(channels) > 0},
		Message:        message,
		MemberCount:    0,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	return n
}

func insertMember(t *testing.T, q *db.Queries, group, username, mobile, work string) {
	t.Helper()
	err := q.InsertGroupMember(context.Background(), db.InsertGroupMemberParams{
		GroupName: group,
		Username:  username,
		Mobile:    sql.NullString{String: mobile, Valid: mobile != ""},
		Work:      sql.NullString{String: work, Valid: work != ""},
		SyncedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}
}

// insertSubscription registers username+phone in sms_subscriptions so the
// dispatcher gate allows SMS delivery for that user/number.
func insertSubscription(t *testing.T, q *db.Queries, username, phone string) {
	t.Helper()
	err := q.InsertSMSSubscription(context.Background(), db.InsertSMSSubscriptionParams{
		Username:     username,
		Phone:        phone,
		SubscribedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
}

// ---- Tests ----

func TestDispatcher_SMS(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-SMS", []string{"grp-a"}, []string{"sms"}, "fire alarm")
	insertMember(t, q, "grp-a", "alice", "+15551111111", "")
	insertMember(t, q, "grp-a", "bob", "+15552222222", "")
	insertSubscription(t, q, "alice", "+15551111111")
	insertSubscription(t, q, "bob", "+15552222222")

	sms := &stubSMS{sid: "SM001", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	sms.mu.Lock()
	defer sms.mu.Unlock()
	if len(sms.calls) != 2 {
		t.Fatalf("want 2 SMS calls, got %d", len(sms.calls))
	}

	// Verify member_count updated.
	updated, err := q.GetNotificationByNotificationID(context.Background(), notif.NotificationID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if updated.MemberCount != 2 {
		t.Errorf("want member_count=2, got %d", updated.MemberCount)
	}

	// Verify delivery rows.
	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 2 {
		t.Fatalf("want 2 delivery rows, got %d", len(deliveries))
	}
	for _, del := range deliveries {
		if del.Status != "queued" {
			t.Errorf("want status=queued, got %q", del.Status)
		}
		if del.Channel != "sms" {
			t.Errorf("want channel=sms, got %q", del.Channel)
		}
	}
}

func TestDispatcher_Voice(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-VOICE", []string{"grp-b"}, []string{"voice"}, "alert")
	insertMember(t, q, "grp-b", "alice", "", "+15550001111") // work only — call work number
	insertMember(t, q, "grp-b", "bob", "+15552222222", "")   // mobile only — call mobile number

	voice := &stubVoice{sid: "CA001", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, voice, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	voice.mu.Lock()
	defer voice.mu.Unlock()
	// Both members qualify: alice via work, bob via mobile.
	if len(voice.calls) != 2 {
		t.Fatalf("want 2 voice calls, got %d", len(voice.calls))
	}
	called := make(map[string]bool, 2)
	for _, c := range voice.calls {
		called[c.to] = true
	}
	if !called["+15550001111"] {
		t.Errorf("want alice's work number +15550001111 to be called")
	}
	if !called["+15552222222"] {
		t.Errorf("want bob's mobile number +15552222222 to be called")
	}
}

func TestDispatcher_SMSAndVoice(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-BOTH", []string{"grp-c"}, []string{"sms", "voice"}, "dual alert")
	insertMember(t, q, "grp-c", "alice", "+15551111111", "+15550001111")
	insertSubscription(t, q, "alice", "+15551111111")

	sms := &stubSMS{sid: "SM002", status: "queued"}
	voice := &stubVoice{sid: "CA002", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, voice, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	sms.mu.Lock()
	if len(sms.calls) != 1 {
		t.Errorf("want 1 SMS call, got %d", len(sms.calls))
	}
	sms.mu.Unlock()

	voice.mu.Lock()
	if len(voice.calls) != 1 {
		t.Errorf("want 1 voice call, got %d", len(voice.calls))
	}
	voice.mu.Unlock()

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 2 {
		t.Errorf("want 2 delivery rows (1 sms + 1 voice), got %d", len(deliveries))
	}
}

func TestDispatcher_NilProvider(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-NIL", []string{"grp-d"}, []string{"sms"}, "alert")
	insertMember(t, q, "grp-d", "alice", "+15551111111", "")

	// sms provider is nil — should not panic, no deliveries created for sms.
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, nil, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 0 {
		t.Errorf("want 0 deliveries when provider is nil, got %d", len(deliveries))
	}
}

func TestDispatcher_ProviderError(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-ERR", []string{"grp-e"}, []string{"sms"}, "alert")
	insertMember(t, q, "grp-e", "alice", "+15551111111", "")
	insertSubscription(t, q, "alice", "+15551111111")

	sms := &stubSMS{err: errors.New("network timeout")}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 1 {
		t.Fatalf("want 1 delivery row on error, got %d", len(deliveries))
	}
	if deliveries[0].Status != "failed" {
		t.Errorf("want status=failed, got %q", deliveries[0].Status)
	}
	if !deliveries[0].ErrorMessage.Valid {
		t.Error("want error_message to be set")
	}
}

func TestDispatcher_MultipleGroups_MemberCount(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-MULTI", []string{"grp-f", "grp-g"}, []string{"sms"}, "alert")
	insertMember(t, q, "grp-f", "alice", "+15551111111", "")
	insertMember(t, q, "grp-g", "bob", "+15552222222", "")
	insertSubscription(t, q, "alice", "+15551111111")
	insertSubscription(t, q, "bob", "+15552222222")

	sms := &stubSMS{sid: "SM003", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	updated, _ := q.GetNotificationByNotificationID(context.Background(), notif.NotificationID)
	if updated.MemberCount != 2 {
		t.Errorf("want member_count=2 across 2 groups, got %d", updated.MemberCount)
	}

	sms.mu.Lock()
	if len(sms.calls) != 2 {
		t.Errorf("want 2 SMS calls, got %d", len(sms.calls))
	}
	sms.mu.Unlock()
}

// TestDispatcher_DeliveryConcurrencyBound verifies that the dispatcher never
// exceeds DeliveryConcurrency simultaneous in-flight channel sends even when
// many members are targeted at once.
func TestDispatcher_DeliveryConcurrencyBound(t *testing.T) {
	_, q := testutil.OpenDB(t)

	const (
		memberCount = 10
		bound       = 3
	)

	notif := insertNotification(t, q, "EVT-BOUND", []string{"grp-bound"}, []string{"sms"}, "alert")
	for i := range memberCount {
		insertMember(t, q, "grp-bound", fmt.Sprintf("user%d", i), fmt.Sprintf("+1555%07d", i), "")
	}

	var (
		active   atomic.Int32
		exceeded atomic.Bool
	)

	sms := &funcSMS{
		sendFn: func(to, message string) (string, string, error) {
			n := active.Add(1)
			defer active.Add(-1)
			if int(n) > bound {
				exceeded.Store(true)
			}
			// Hold the slot briefly so goroutines overlap.
			time.Sleep(5 * time.Millisecond)
			return "SM-bound", "queued", nil
		},
	}

	cfg := config.NotifyConfig{
		WorkerCount:         1,
		RetryLimit:          1,
		RetryDelay:          0,
		DeliveryTimeout:     5 * time.Second,
		DeliveryConcurrency: bound,
	}
	d := NewDispatcher(cfg, q, make(chan Job, 1), sms, nil, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	if exceeded.Load() {
		t.Errorf("concurrency bound of %d was exceeded", bound)
	}

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != memberCount {
		t.Errorf("want %d delivery rows, got %d", memberCount, len(deliveries))
	}
}

// TestMergeEmailVars verifies that notification and event context are always
// injected and that user-supplied vars coexist at the top level.
func TestMergeEmailVars(t *testing.T) {
	notif := db.Notification{
		NotificationID: "notif-abc",
		EventID:        "evt-xyz",
		Message:        "disk nearly full",
		Groups:         sql.NullString{String: `["oncall","ops"]`, Valid: true},
		CreatedAt:      "2026-04-16T10:00:00Z",
	}
	event := db.Event{
		EventID:          "evt-xyz",
		EventName:        sql.NullString{String: "Disk Alert", Valid: true},
		EventSeverity:    sql.NullString{String: "major", Valid: true},
		EventUrl:         sql.NullString{String: "https://example.com/evt-xyz", Valid: true},
		EventDescription: sql.NullString{String: "NVMe filling up", Valid: true},
		StartTime:        "2026-04-16T09:50:00Z",
		EndTime:          sql.NullString{},
	}
	userVars := map[string]any{"custom": "hello", "count": 42}

	merged := mergeEmailVars(userVars, notif, event)

	// User vars preserved.
	if merged["custom"] != "hello" {
		t.Errorf("custom = %v, want %q", merged["custom"], "hello")
	}
	if merged["count"] != 42 {
		t.Errorf("count = %v, want 42", merged["count"])
	}

	// notification sub-object.
	n, ok := merged["notification"].(map[string]any)
	if !ok {
		t.Fatalf("merged[\"notification\"] is %T, want map[string]any", merged["notification"])
	}
	if n["id"] != "notif-abc" {
		t.Errorf("notification.id = %v, want %q", n["id"], "notif-abc")
	}
	if n["message"] != "disk nearly full" {
		t.Errorf("notification.message = %v", n["message"])
	}
	groups, _ := n["groups"].([]string)
	if len(groups) != 2 || groups[0] != "oncall" {
		t.Errorf("notification.groups = %v, want [oncall ops]", groups)
	}

	// event sub-object.
	e, ok := merged["event"].(map[string]any)
	if !ok {
		t.Fatalf("merged[\"event\"] is %T, want map[string]any", merged["event"])
	}
	if e["id"] != "evt-xyz" {
		t.Errorf("event.id = %v, want %q", e["id"], "evt-xyz")
	}
	if e["severity"] != "major" {
		t.Errorf("event.severity = %v, want %q", e["severity"], "major")
	}
	if e["name"] != "Disk Alert" {
		t.Errorf("event.name = %v, want %q", e["name"], "Disk Alert")
	}
	if e["end_time"] != "" {
		t.Errorf("event.end_time = %v, want empty string for null", e["end_time"])
	}
}

// TestMergeEmailVars_UserKeyOverriddenByContext verifies that if a user
// supplies a key named "notification" or "event", it is replaced by the real
// context data.
func TestMergeEmailVars_UserKeyOverriddenByContext(t *testing.T) {
	notif := db.Notification{
		NotificationID: "notif-1",
		EventID:        "evt-1",
		Message:        "test",
		Groups:         sql.NullString{String: `[]`, Valid: true},
		CreatedAt:      "2026-04-16T00:00:00Z",
	}
	event := db.Event{EventID: "evt-1", StartTime: "2026-04-16T00:00:00Z"}
	userVars := map[string]any{
		"notification": "user-supplied-value",
		"event":        "user-supplied-value",
	}

	merged := mergeEmailVars(userVars, notif, event)

	if _, ok := merged["notification"].(map[string]any); !ok {
		t.Error("\"notification\" key should be the context map, not the user string")
	}
	if _, ok := merged["event"].(map[string]any); !ok {
		t.Error("\"event\" key should be the context map, not the user string")
	}
}

// insertNotificationWithDestinations creates a notification with destinations (no groups).
func insertNotificationWithDestinations(t *testing.T, q *db.Queries, eventID string, destinations []notifDestination, message string) db.Notification {
	t.Helper()
	ctx := context.Background()

	destsJSON, _ := json.Marshal(destinations)

	if _, err := q.GetEventByEventID(ctx, eventID); errors.Is(err, sql.ErrNoRows) {
		q.InsertEvent(ctx, db.InsertEventParams{
			EventID:   eventID,
			StartTime: time.Now().UTC().Format(time.RFC3339),
		})
	}

	n, err := q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: uuidV7(),
		EventID:        eventID,
		Destinations:   sql.NullString{String: string(destsJSON), Valid: true},
		Message:        message,
		MemberCount:    0,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insert notification with destinations: %v", err)
	}
	return n
}

func TestDispatcher_DestinationSMS(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotificationWithDestinations(t, q, "EVT-DEST-SMS",
		[]notifDestination{{Channel: "sms", Target: "+15559990001"}},
		"destination alert",
	)
	insertSubscription(t, q, "dest-user", "+15559990001")

	sms := &stubSMS{sid: "SM-DEST-001", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	sms.mu.Lock()
	defer sms.mu.Unlock()
	if len(sms.calls) != 1 {
		t.Fatalf("want 1 SMS call, got %d", len(sms.calls))
	}
	if sms.calls[0].to != "+15559990001" {
		t.Errorf("want to=+15559990001, got %q", sms.calls[0].to)
	}

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 1 {
		t.Fatalf("want 1 delivery row, got %d", len(deliveries))
	}
	del := deliveries[0]
	if del.Group.Valid {
		t.Errorf("want group=NULL for destination delivery, got %q", del.Group.String)
	}
	if del.Member.Valid {
		t.Errorf("want member=NULL for destination delivery, got %q", del.Member.String)
	}
	if !del.Destination.Valid || del.Destination.String != "+15559990001" {
		t.Errorf("want destination=+15559990001, got %v", del.Destination)
	}
	if del.Channel != "sms" {
		t.Errorf("want channel=sms, got %q", del.Channel)
	}
	if del.Status != "queued" {
		t.Errorf("want status=queued, got %q", del.Status)
	}

	updated, _ := q.GetNotificationByNotificationID(context.Background(), notif.NotificationID)
	if updated.MemberCount != 1 {
		t.Errorf("want member_count=1, got %d", updated.MemberCount)
	}
	if updated.Status != "completed" {
		t.Errorf("want status=completed, got %q", updated.Status)
	}
}

func TestDispatcher_DestinationVoice(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotificationWithDestinations(t, q, "EVT-DEST-VOICE",
		[]notifDestination{{Channel: "voice", Target: "+15559990002"}},
		"voice alert",
	)

	voice := &stubVoice{sid: "CA-DEST-001", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, voice, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.calls) != 1 {
		t.Fatalf("want 1 voice call, got %d", len(voice.calls))
	}
	if voice.calls[0].to != "+15559990002" {
		t.Errorf("want to=+15559990002, got %q", voice.calls[0].to)
	}

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 1 {
		t.Fatalf("want 1 delivery row, got %d", len(deliveries))
	}
	if !deliveries[0].Destination.Valid || deliveries[0].Destination.String != "+15559990002" {
		t.Errorf("want destination=+15559990002, got %v", deliveries[0].Destination)
	}
}

func TestDispatcher_DestinationNilProvider(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotificationWithDestinations(t, q, "EVT-DEST-NIL",
		[]notifDestination{{Channel: "sms", Target: "+15559990003"}},
		"alert",
	)

	// nil sms provider — should not panic, 0 deliveries.
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, nil, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	deliveries, _ := q.ListDeliveriesByNotificationID(context.Background(), notif.NotificationID)
	if len(deliveries) != 0 {
		t.Errorf("want 0 deliveries when provider is nil, got %d", len(deliveries))
	}
}

func TestDispatcher_MixedGroupAndDestination(t *testing.T) {
	_, q := testutil.OpenDB(t)
	ctx := context.Background()

	// Ensure event exists.
	if _, err := q.GetEventByEventID(ctx, "EVT-MIXED"); errors.Is(err, sql.ErrNoRows) {
		q.InsertEvent(ctx, db.InsertEventParams{EventID: "EVT-MIXED", StartTime: time.Now().UTC().Format(time.RFC3339)})
	}

	destsJSON, _ := json.Marshal([]notifDestination{{Channel: "sms", Target: "+15559990004"}})
	groupsJSON, _ := json.Marshal([]string{"grp-mixed"})
	channelsJSON, _ := json.Marshal([]string{"sms"})

	notif, err := q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: uuidV7(),
		EventID:        "EVT-MIXED",
		Groups:         sql.NullString{String: string(groupsJSON), Valid: true},
		Destinations:   sql.NullString{String: string(destsJSON), Valid: true},
		Channels:       sql.NullString{String: string(channelsJSON), Valid: true},
		Message:        "mixed alert",
		MemberCount:    0,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	insertMember(t, q, "grp-mixed", "dave", "+15559990005", "")
	insertSubscription(t, q, "dave", "+15559990005")
	insertSubscription(t, q, "dest-mixed", "+15559990004")

	sms := &stubSMS{sid: "SM-MIX", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())
	d.processJob(ctx, Job{NotificationID: notif.NotificationID})

	sms.mu.Lock()
	if len(sms.calls) != 2 {
		t.Errorf("want 2 SMS calls (1 group + 1 destination), got %d", len(sms.calls))
	}
	sms.mu.Unlock()

	deliveries, _ := q.ListDeliveriesByNotificationID(ctx, notif.NotificationID)
	if len(deliveries) != 2 {
		t.Fatalf("want 2 delivery rows, got %d", len(deliveries))
	}

	var groupDel, destDel *db.Delivery
	for i := range deliveries {
		if deliveries[i].Group.Valid {
			groupDel = &deliveries[i]
		} else {
			destDel = &deliveries[i]
		}
	}
	if groupDel == nil {
		t.Error("want one group delivery row")
	}
	if destDel == nil {
		t.Error("want one destination delivery row")
	}

	updated, _ := q.GetNotificationByNotificationID(ctx, notif.NotificationID)
	if updated.MemberCount != 2 {
		t.Errorf("want member_count=2 (1 member + 1 destination), got %d", updated.MemberCount)
	}
}

func TestDispatcher_DestinationOnly_MemberCount(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotificationWithDestinations(t, q, "EVT-DEST-COUNT",
		[]notifDestination{
			{Channel: "sms", Target: "+15559990010"},
			{Channel: "sms", Target: "+15559990011"},
		},
		"count test",
	)

	sms := &stubSMS{sid: "SM-CNT", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), sms, nil, nil, noopLogger())
	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	updated, _ := q.GetNotificationByNotificationID(context.Background(), notif.NotificationID)
	if updated.MemberCount != 2 {
		t.Errorf("want member_count=2, got %d", updated.MemberCount)
	}
	if updated.Status != "completed" {
		t.Errorf("want status=completed, got %q", updated.Status)
	}
}

// TestDispatcher_Email_ContextVars runs a full dispatchEmail cycle and
// verifies that the rendered email body contains values from the notification
// and event context (injected automatically, not provided in email_vars).
func TestDispatcher_Email_ContextVars(t *testing.T) {
	_, q := testutil.OpenDB(t)
	ctx := context.Background()

	// Create an event with specific fields.
	event, err := q.InsertEvent(ctx, db.InsertEventParams{
		EventID:       "EVT-CTX",
		EventName:     sql.NullString{String: "Power Outage", Valid: true},
		EventSeverity: sql.NullString{String: "critical", Valid: true},
		StartTime:     "2026-04-16T08:00:00Z",
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Create an email template that references context vars.
	tmpl, err := q.InsertEmailTemplate(ctx, db.InsertEmailTemplateParams{
		TemplateName: "ctx-test",
		Subject:      "Alert: {{.event.severity}} - {{.event.name}}",
		Body:         "<p>{{.notification.message}}</p><p>Custom: {{.custom}}</p>",
		RequiredVars: `["custom"]`,
	})
	if err != nil {
		t.Fatalf("insert template: %v", err)
	}

	// Insert notification referencing that template and a user var.
	varsJSON, _ := json.Marshal(map[string]any{"custom": "my-value"})
	groupsJSON, _ := json.Marshal([]string{"grp-email"})
	channelsJSON, _ := json.Marshal([]string{"email"})
	notif, err := q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: uuidV7(),
		EventID:        event.EventID,
		Groups:         sql.NullString{String: string(groupsJSON), Valid: true},
		Channels:       sql.NullString{String: string(channelsJSON), Valid: true},
		Message:        "generator offline",
		MemberCount:    0,
		EmailTemplate:  sql.NullString{String: tmpl.TemplateName, Valid: true},
		EmailVars:      sql.NullString{String: string(varsJSON), Valid: true},
		CreatedAt:      "2026-04-16T08:05:00Z",
	})
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	// Insert a member with an email address.
	err = q.InsertGroupMember(ctx, db.InsertGroupMemberParams{
		GroupName: "grp-email",
		Username:  "carol",
		Email:     sql.NullString{String: "carol@example.com", Valid: true},
		SyncedAt:  "2026-04-16T08:00:00Z",
	})
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	email := &stubEmail{}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, nil, email, noopLogger())
	d.processJob(ctx, Job{NotificationID: notif.NotificationID})

	email.mu.Lock()
	defer email.mu.Unlock()

	if len(email.calls) != 1 {
		t.Fatalf("want 1 email sent, got %d", len(email.calls))
	}
	call := email.calls[0]
	if call.to != "carol@example.com" {
		t.Errorf("to = %q, want %q", call.to, "carol@example.com")
	}
	wantSubject := "Alert: critical - Power Outage"
	if call.subject != wantSubject {
		t.Errorf("subject = %q, want %q", call.subject, wantSubject)
	}
	if !strings.Contains(call.body, "generator offline") {
		t.Errorf("body missing notification message: %q", call.body)
	}
	if !strings.Contains(call.body, "my-value") {
		t.Errorf("body missing user var: %q", call.body)
	}
}
