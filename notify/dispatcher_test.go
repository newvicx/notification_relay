package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"notification_relay/config"
	"notification_relay/db"
	"notification_relay/testutil"
)

// ---- Stub providers ----

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
		WorkerCount:     1,
		RetryLimit:      1,
		RetryDelay:      0,
		DeliveryTimeout: 5 * time.Second,
	}
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
		Groups:         string(groupsJSON),
		Channels:       string(channelsJSON),
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

// ---- Tests ----

func TestDispatcher_SMS(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-SMS", []string{"grp-a"}, []string{"sms"}, "fire alarm")
	insertMember(t, q, "grp-a", "alice", "+15551111111", "")
	insertMember(t, q, "grp-a", "bob", "+15552222222", "")

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
	insertMember(t, q, "grp-b", "alice", "", "+15550001111") // has work, no mobile
	insertMember(t, q, "grp-b", "bob", "+15552222222", "")   // has mobile, no work

	voice := &stubVoice{sid: "CA001", status: "queued"}
	d := NewDispatcher(defaultCfg(), q, make(chan Job, 1), nil, voice, nil, noopLogger())

	d.processJob(context.Background(), Job{NotificationID: notif.NotificationID})

	voice.mu.Lock()
	defer voice.mu.Unlock()
	if len(voice.calls) != 1 {
		t.Fatalf("want 1 voice call (only alice has work number), got %d", len(voice.calls))
	}
	if voice.calls[0].to != "+15550001111" {
		t.Errorf("want alice's work number, got %q", voice.calls[0].to)
	}
}

func TestDispatcher_SMSAndVoice(t *testing.T) {
	_, q := testutil.OpenDB(t)
	notif := insertNotification(t, q, "EVT-BOTH", []string{"grp-c"}, []string{"sms", "voice"}, "dual alert")
	insertMember(t, q, "grp-c", "alice", "+15551111111", "+15550001111")

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
