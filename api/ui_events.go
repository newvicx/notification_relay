package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type uiEventRow struct {
	EventID       string
	EventName     string
	EventSeverity string
	StartTime     string
	EndTime       string
}

type uiEventsListData struct {
	Events          []uiEventRow
	Offset          int64
	HasNext         bool
	StartFrom       string
	StartTo         string
	EventID         string
	EventName       string
	Description     string
	Severity        string
	CreatedBy       string
	Status          string
	SeverityOptions []string
	PrevQuery       string
	NextQuery       string
}

func (s *Server) handleUIListEvents(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	events, limit, offset, err := s.listEventsCore(r)
	if err != nil {
		s.renderUIPage(w, "events_list.html", uiPageData{
			Title: "Events", User: user, IsAdmin: isAdminUser(user),
			Flash: err.Error(), FlashClass: "error",
			Data: uiEventsListData{},
		})
		return
	}

	rows := make([]uiEventRow, len(events))
	for i, e := range events {
		rows[i] = uiEventRow{
			EventID:       e.EventID,
			EventName:     e.EventName.String,
			EventSeverity: e.EventSeverity.String,
			StartTime:     e.StartTime,
			EndTime:       e.EndTime.String,
		}
	}

	q := r.URL.Query()
	prevOffset := offset - limit
	if prevOffset < 0 {
		prevOffset = 0
	}
	prevQuery := url.Values{"offset": {strconv.FormatInt(prevOffset, 10)}, "limit": {strconv.FormatInt(limit, 10)}}
	nextQuery := url.Values{"offset": {strconv.FormatInt(offset+limit, 10)}, "limit": {strconv.FormatInt(limit, 10)}}
	for _, k := range []string{"start_from", "start_to", "event_id", "event_name", "description", "severity", "created_by", "status"} {
		if v := q.Get(k); v != "" {
			prevQuery.Set(k, v)
			nextQuery.Set(k, v)
		}
	}

	s.renderUIPage(w, "events_list.html", uiPageData{
		Title: "Events", User: user, IsAdmin: isAdminUser(user),
		Data: uiEventsListData{
			Events:          rows,
			Offset:          offset,
			HasNext:         int64(len(events)) == limit,
			StartFrom:       q.Get("start_from"),
			StartTo:         q.Get("start_to"),
			EventID:         q.Get("event_id"),
			EventName:       q.Get("event_name"),
			Description:     q.Get("description"),
			Severity:        q.Get("severity"),
			CreatedBy:       q.Get("created_by"),
			Status:          q.Get("status"),
			SeverityOptions: s.eventSeverities,
			PrevQuery:       prevQuery.Encode(),
			NextQuery:       nextQuery.Encode(),
		},
	})
}

type uiEventDetail struct {
	EventID          string
	EventName        string
	EventSeverity    string
	EventURL         string
	EventDescription string
	StartTime        string
	EndTime          string
	CreatedBy        string
	CreatedAt        string
}

type uiNotificationRow struct {
	NotificationID string
	Message        string
	Channels       string
	Groups         string
	MemberCount    int64
	Status         string
	ErrorMessage   string
	CreatedAt      string
}

type uiEventDetailData struct {
	Event         uiEventDetail
	Notifications []uiNotificationRow
}

func (s *Server) handleUIGetEvent(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	event, err := s.getEventCore(r)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	notifications, err := s.listEventNotificationsCore(r)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	rows := make([]uiNotificationRow, 0, len(notifications))
	for _, n := range notifications {
		nr, err := toNotificationResponse(n)
		if err != nil {
			s.logger.Error("ui event detail: decode notification failed", "notification_id", n.NotificationID, "error", err)
			continue
		}
		rows = append(rows, uiNotificationRow{
			NotificationID: nr.NotificationID,
			Message:        nr.Message,
			Channels:       strings.Join(nr.Channels, ", "),
			Groups:         strings.Join(nr.Groups, ", "),
			MemberCount:    nr.MemberCount,
			Status:         nr.Status,
			ErrorMessage:   nr.ErrorMessage,
			CreatedAt:      nr.CreatedAt,
		})
	}

	s.renderUIPage(w, "event_detail.html", uiPageData{
		Title: event.EventName.String, User: user, IsAdmin: isAdminUser(user),
		Data: uiEventDetailData{
			Event: uiEventDetail{
				EventID:          event.EventID,
				EventName:        event.EventName.String,
				EventSeverity:    event.EventSeverity.String,
				EventURL:         event.EventUrl.String,
				EventDescription: event.EventDescription.String,
				StartTime:        event.StartTime,
				EndTime:          event.EndTime.String,
				CreatedBy:        event.CreatedBy.String,
				CreatedAt:        event.CreatedAt,
			},
			Notifications: rows,
		},
	})
}

func (s *Server) handleUINotificationDeliveriesPartial(w http.ResponseWriter, r *http.Request) {
	deliveries, err := s.listNotificationDeliveriesCore(r)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	resp := make([]deliveryResponse, 0, len(deliveries))
	for _, d := range deliveries {
		dr, err := toDeliveryResponse(d)
		if err != nil {
			s.logger.Error("ui deliveries partial: decode failed", "delivery_id", d.DeliveryID, "error", err)
			continue
		}
		resp = append(resp, dr)
	}
	s.renderUIFragment(w, "deliveries_fragment.html", resp)
}
