package adminaudit

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
)

const (
	DefaultCapacity = 1000
	DefaultLimit    = 100
	MaxLimit        = 1000
)

type Recorder interface {
	RecordAdminAudit(Entry) Entry
}

type Lister interface {
	List(Query) Result
}

type Trail interface {
	Recorder
	Lister
}

type StoreConfig struct {
	Capacity int
}

type Store struct {
	mu       sync.RWMutex
	now      func() time.Time
	capacity int
	nextID   uint64
	entries  []Entry
}

type Entry struct {
	ID              uint64            `json:"id"`
	RecordedAt      time.Time         `json:"recorded_at"`
	Action          string            `json:"action"`
	Result          string            `json:"result"`
	HTTPStatus      int               `json:"http_status,omitempty"`
	GatewayNode     string            `json:"gateway_node,omitempty"`
	AuthMode        string            `json:"auth_mode,omitempty"`
	Principal       string            `json:"principal,omitempty"`
	Role            string            `json:"role,omitempty"`
	AdminSessionID  string            `json:"admin_session_id,omitempty"`
	AuthKeyID       string            `json:"auth_key_id,omitempty"`
	Method          string            `json:"method,omitempty"`
	Path            string            `json:"path,omitempty"`
	RemoteAddr      string            `json:"remote_addr,omitempty"`
	Permission      string            `json:"permission,omitempty"`
	TargetClientID  string            `json:"target_client_id,omitempty"`
	TargetDeviceID  string            `json:"target_device_id,omitempty"`
	TargetSessionID string            `json:"target_session_id,omitempty"`
	TargetConnID    uint64            `json:"target_conn_id,omitempty"`
	MessageID       string            `json:"message_id,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	Details         map[string]string `json:"details,omitempty"`
}

type Query struct {
	Limit     int
	Cursor    uint64
	Action    string
	Result    string
	Principal string
	ClientID  string
	SessionID string
	MessageID string
}

type Result struct {
	Limit      int
	Cursor     uint64
	NextCursor uint64
	HasMore    bool
	Total      int
	Entries    []Entry
}

func NewStore(config StoreConfig) *Store {
	capacity := config.Capacity
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{
		now:      time.Now,
		capacity: capacity,
		entries:  make([]Entry, 0, capacity),
	}
}

func Record(recorder Recorder, entry Entry) Entry {
	entry = normalizeEntry(entry)
	metrics.RecordAdminAction(entry.Action, entry.Result)
	if recorder == nil {
		return entry
	}
	return recorder.RecordAdminAudit(entry)
}

func (s *Store) RecordAdminAudit(entry Entry) Entry {
	if s == nil {
		return normalizeEntry(entry)
	}
	entry = normalizeEntry(entry)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	entry.ID = s.nextID
	if entry.RecordedAt.IsZero() {
		entry.RecordedAt = s.now().UTC()
	} else {
		entry.RecordedAt = entry.RecordedAt.UTC()
	}

	if len(s.entries) >= s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
		return cloneEntry(entry)
	}
	s.entries = append(s.entries, entry)
	return cloneEntry(entry)
}

func (s *Store) List(query Query) Result {
	query = normalizeQuery(query)
	if s == nil {
		return Result{Limit: query.Limit, Cursor: query.Cursor}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := Result{
		Limit:   query.Limit,
		Cursor:  query.Cursor,
		Entries: make([]Entry, 0, query.Limit),
	}
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := s.entries[i]
		if !entryMatches(entry, query) {
			continue
		}
		result.Total++
		if query.Cursor > 0 && entry.ID >= query.Cursor {
			continue
		}
		if len(result.Entries) < query.Limit {
			result.Entries = append(result.Entries, cloneEntry(entry))
			continue
		}
		if query.Limit > 0 {
			result.HasMore = true
		}
	}
	if result.HasMore && len(result.Entries) > 0 {
		result.NextCursor = result.Entries[len(result.Entries)-1].ID
	}
	return result
}

func (s *Store) Ping(ctx context.Context) error {
	return ctx.Err()
}

func QueryFromValues(values url.Values) Query {
	return Query{
		Limit:     parseLimit(values.Get("limit")),
		Cursor:    parseCursor(values.Get("cursor")),
		Action:    strings.TrimSpace(values.Get("action")),
		Result:    strings.TrimSpace(values.Get("result")),
		Principal: strings.TrimSpace(values.Get("principal")),
		ClientID:  strings.TrimSpace(values.Get("client_id")),
		SessionID: strings.TrimSpace(values.Get("session_id")),
		MessageID: strings.TrimSpace(values.Get("message_id")),
	}
}

func normalizeQuery(query Query) Query {
	query.Action = strings.TrimSpace(query.Action)
	query.Result = strings.TrimSpace(query.Result)
	query.Principal = strings.TrimSpace(query.Principal)
	query.ClientID = strings.TrimSpace(query.ClientID)
	query.SessionID = strings.TrimSpace(query.SessionID)
	query.MessageID = strings.TrimSpace(query.MessageID)
	query.Limit = clampLimit(query.Limit)
	if query.Cursor > maxCursor {
		query.Cursor = maxCursor
	}
	return query
}

func normalizeEntry(entry Entry) Entry {
	entry.Action = nonEmpty(strings.TrimSpace(entry.Action), "unknown")
	entry.Result = nonEmpty(strings.TrimSpace(entry.Result), "unknown")
	entry.GatewayNode = strings.TrimSpace(entry.GatewayNode)
	entry.AuthMode = strings.TrimSpace(entry.AuthMode)
	entry.Principal = strings.TrimSpace(entry.Principal)
	entry.Role = strings.TrimSpace(entry.Role)
	entry.AdminSessionID = strings.TrimSpace(entry.AdminSessionID)
	entry.AuthKeyID = strings.TrimSpace(entry.AuthKeyID)
	entry.Method = strings.TrimSpace(entry.Method)
	entry.Path = strings.TrimSpace(entry.Path)
	entry.RemoteAddr = strings.TrimSpace(entry.RemoteAddr)
	entry.Permission = strings.TrimSpace(entry.Permission)
	entry.TargetClientID = strings.TrimSpace(entry.TargetClientID)
	entry.TargetDeviceID = strings.TrimSpace(entry.TargetDeviceID)
	entry.TargetSessionID = strings.TrimSpace(entry.TargetSessionID)
	entry.MessageID = strings.TrimSpace(entry.MessageID)
	entry.TraceID = strings.TrimSpace(entry.TraceID)
	entry.Reason = strings.TrimSpace(entry.Reason)
	if len(entry.Details) == 0 {
		entry.Details = nil
		return entry
	}

	details := make(map[string]string, len(entry.Details))
	for key, value := range entry.Details {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		details[key] = strings.TrimSpace(value)
	}
	if len(details) == 0 {
		entry.Details = nil
		return entry
	}
	entry.Details = details
	return entry
}

func entryMatches(entry Entry, query Query) bool {
	if query.Action != "" && entry.Action != query.Action {
		return false
	}
	if query.Result != "" && entry.Result != query.Result {
		return false
	}
	if query.Principal != "" && !strings.Contains(entry.Principal, query.Principal) {
		return false
	}
	if query.ClientID != "" && entry.TargetClientID != query.ClientID {
		return false
	}
	if query.SessionID != "" && entry.TargetSessionID != query.SessionID && entry.AdminSessionID != query.SessionID {
		return false
	}
	if query.MessageID != "" && entry.MessageID != query.MessageID {
		return false
	}
	return true
}

func cloneEntry(entry Entry) Entry {
	if len(entry.Details) == 0 {
		entry.Details = nil
		return entry
	}
	details := make(map[string]string, len(entry.Details))
	for key, value := range entry.Details {
		details[key] = value
	}
	entry.Details = details
	return entry
}

func parseLimit(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultLimit
	}
	limit := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return DefaultLimit
		}
		limit = limit*10 + int(r-'0')
		if limit > MaxLimit {
			return MaxLimit
		}
	}
	return clampLimit(limit)
}

func parseCursor(raw string) uint64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}

	var cursor uint64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		digit := uint64(r - '0')
		if cursor > (maxCursor-digit)/10 {
			return maxCursor
		}
		cursor = cursor*10 + digit
	}
	return cursor
}

const maxCursor = uint64(1<<63 - 1)

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
