package adminaudit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStoreConfig struct {
	DSN              string
	AutoMigrate      bool
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	OperationTimeout time.Duration
}

type PostgresStore struct {
	db               *sql.DB
	operationTimeout time.Duration
}

const postgresAuditColumns = `
id, recorded_at, action, result, http_status, gateway_node, auth_mode,
principal, role, admin_session_id, auth_key_id, method, path, remote_addr,
permission, target_client_id, target_device_id, target_session_id,
target_conn_id, message_id, trace_id, reason, details
`

func NewPostgresStore(ctx context.Context, config PostgresStoreConfig) (*PostgresStore, error) {
	if strings.TrimSpace(config.DSN) == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}

	db, err := sql.Open("pgx", config.DSN)
	if err != nil {
		return nil, err
	}
	if config.MaxOpenConns > 0 {
		db.SetMaxOpenConns(config.MaxOpenConns)
	}
	if config.MaxIdleConns > 0 {
		db.SetMaxIdleConns(config.MaxIdleConns)
	}
	if config.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(config.ConnMaxLifetime)
	}

	timeout := config.OperationTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	store := &PostgresStore{
		db:               db,
		operationTimeout: timeout,
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if config.AutoMigrate {
		if err := store.Migrate(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	return store, nil
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres audit store is not configured")
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS z_courier_admin_audit_events (
  id BIGSERIAL PRIMARY KEY,
  recorded_at TIMESTAMPTZ NOT NULL,
  action TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  http_status INTEGER NOT NULL DEFAULT 0,
  gateway_node TEXT NOT NULL DEFAULT '',
  auth_mode TEXT NOT NULL DEFAULT '',
  principal TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
  admin_session_id TEXT NOT NULL DEFAULT '',
  auth_key_id TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  remote_addr TEXT NOT NULL DEFAULT '',
  permission TEXT NOT NULL DEFAULT '',
  target_client_id TEXT NOT NULL DEFAULT '',
  target_device_id TEXT NOT NULL DEFAULT '',
  target_session_id TEXT NOT NULL DEFAULT '',
  target_conn_id BIGINT NOT NULL DEFAULT 0,
  message_id TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  details JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_recorded_at_idx
  ON z_courier_admin_audit_events (recorded_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_action_result_idx
  ON z_courier_admin_audit_events (action, result, recorded_at DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_principal_idx
  ON z_courier_admin_audit_events (principal, recorded_at DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_target_client_idx
  ON z_courier_admin_audit_events (target_client_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_target_session_idx
  ON z_courier_admin_audit_events (target_session_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_admin_session_idx
  ON z_courier_admin_audit_events (admin_session_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS z_courier_admin_audit_message_idx
  ON z_courier_admin_audit_events (message_id, recorded_at DESC);
`)
	return err
}

func (s *PostgresStore) RecordAdminAudit(entry Entry) Entry {
	entry = normalizeEntry(entry)
	if s == nil || s.db == nil {
		return cloneEntry(entry)
	}
	if entry.RecordedAt.IsZero() {
		entry.RecordedAt = time.Now().UTC()
	} else {
		entry.RecordedAt = entry.RecordedAt.UTC()
	}

	details, err := marshalDetails(entry.Details)
	if err != nil {
		entry.Details = map[string]string{"details_error": err.Error()}
		details, _ = marshalDetails(entry.Details)
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.operationTimeout)
	defer cancel()

	var id int64
	var recordedAt time.Time
	err = s.db.QueryRowContext(ctx, `
INSERT INTO z_courier_admin_audit_events (
  recorded_at, action, result, http_status, gateway_node, auth_mode,
  principal, role, admin_session_id, auth_key_id, method, path, remote_addr,
  permission, target_client_id, target_device_id, target_session_id,
  target_conn_id, message_id, trace_id, reason, details
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11, $12, $13,
  $14, $15, $16, $17,
  $18, $19, $20, $21, $22
)
RETURNING id, recorded_at
`,
		entry.RecordedAt,
		entry.Action,
		entry.Result,
		entry.HTTPStatus,
		entry.GatewayNode,
		entry.AuthMode,
		entry.Principal,
		entry.Role,
		entry.AdminSessionID,
		entry.AuthKeyID,
		entry.Method,
		entry.Path,
		entry.RemoteAddr,
		entry.Permission,
		entry.TargetClientID,
		entry.TargetDeviceID,
		entry.TargetSessionID,
		int64(entry.TargetConnID),
		entry.MessageID,
		entry.TraceID,
		entry.Reason,
		details,
	).Scan(&id, &recordedAt)
	if err != nil {
		return cloneEntry(entry)
	}

	if id > 0 {
		entry.ID = uint64(id)
	}
	entry.RecordedAt = recordedAt.UTC()
	return cloneEntry(entry)
}

func (s *PostgresStore) List(query Query) Result {
	query = normalizeQuery(query)
	result := Result{
		Limit:   query.Limit,
		Entries: make([]Entry, 0, query.Limit),
	}
	if s == nil || s.db == nil {
		return result
	}

	where, args := postgresAuditWhere(query)
	ctx, cancel := context.WithTimeout(context.Background(), s.operationTimeout)
	defer cancel()

	countQuery := "SELECT COUNT(*) FROM z_courier_admin_audit_events" + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&result.Total); err != nil {
		return Result{Limit: query.Limit}
	}

	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT `+postgresAuditColumns+`
FROM z_courier_admin_audit_events
`+where+`
ORDER BY recorded_at DESC, id DESC
LIMIT $`+fmt.Sprint(len(args))+`
`, args...)
	if err != nil {
		return Result{Limit: query.Limit, Total: result.Total}
	}
	defer rows.Close()

	for rows.Next() {
		entry, err := scanAuditEntry(rows)
		if err != nil {
			return Result{Limit: query.Limit, Total: result.Total, Entries: result.Entries}
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Result{Limit: query.Limit, Total: result.Total, Entries: result.Entries}
	}
	return result
}

func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func postgresAuditWhere(query Query) (string, []any) {
	var conditions []string
	var args []any
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}

	if query.Action != "" {
		add("action = $%d", query.Action)
	}
	if query.Result != "" {
		add("result = $%d", query.Result)
	}
	if query.Principal != "" {
		add("principal LIKE '%%' || $%d || '%%'", query.Principal)
	}
	if query.ClientID != "" {
		add("target_client_id = $%d", query.ClientID)
	}
	if query.SessionID != "" {
		args = append(args, query.SessionID)
		index := len(args)
		conditions = append(conditions, fmt.Sprintf("(target_session_id = $%d OR admin_session_id = $%d)", index, index))
	}
	if query.MessageID != "" {
		add("message_id = $%d", query.MessageID)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func scanAuditEntry(row rowScanner) (Entry, error) {
	var entry Entry
	var details []byte
	var id int64
	var targetConnID int64
	if err := row.Scan(
		&id,
		&entry.RecordedAt,
		&entry.Action,
		&entry.Result,
		&entry.HTTPStatus,
		&entry.GatewayNode,
		&entry.AuthMode,
		&entry.Principal,
		&entry.Role,
		&entry.AdminSessionID,
		&entry.AuthKeyID,
		&entry.Method,
		&entry.Path,
		&entry.RemoteAddr,
		&entry.Permission,
		&entry.TargetClientID,
		&entry.TargetDeviceID,
		&entry.TargetSessionID,
		&targetConnID,
		&entry.MessageID,
		&entry.TraceID,
		&entry.Reason,
		&details,
	); err != nil {
		return Entry{}, err
	}
	if targetConnID > 0 {
		entry.TargetConnID = uint64(targetConnID)
	}
	if id > 0 {
		entry.ID = uint64(id)
	}
	if len(details) > 0 && string(details) != "{}" {
		var decoded map[string]string
		if err := sonic.Unmarshal(details, &decoded); err != nil {
			return Entry{}, err
		}
		entry.Details = decoded
	}
	return cloneEntry(normalizeEntry(entry)), nil
}

func marshalDetails(details map[string]string) ([]byte, error) {
	if len(details) == 0 {
		return []byte("{}"), nil
	}
	return sonic.Marshal(details)
}

type rowScanner interface {
	Scan(...any) error
}
