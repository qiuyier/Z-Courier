package downlink

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStoreConfig struct {
	DSN             string
	AutoMigrate     bool
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type PostgresStore struct {
	db *sql.DB
}

const postgresMessageColumns = `
message_id, client_id, device_id, msg_id, body, ack_required, trace_id,
session_id, status, attempts, next_retry_at, last_error, created_at,
updated_at, sent_at, delivered_at, claim_owner, claim_until
`

func NewPostgresStore(ctx context.Context, config PostgresStoreConfig) (*PostgresStore, error) {
	if config.DSN == "" {
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

	store := &PostgresStore{db: db}
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
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS z_courier_downlink_messages (
  message_id TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  msg_id INTEGER NOT NULL,
  body BYTEA NOT NULL DEFAULT ''::bytea,
  ack_required BOOLEAN NOT NULL DEFAULT false,
  trace_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  sent_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  claim_owner TEXT NOT NULL DEFAULT '',
  claim_until TIMESTAMPTZ
);
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS claim_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS claim_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_client_device_status_idx
  ON z_courier_downlink_messages (client_id, device_id, status);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_next_retry_at_idx
  ON z_courier_downlink_messages (next_retry_at)
  WHERE next_retry_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_claim_until_idx
  ON z_courier_downlink_messages (claim_until)
  WHERE claim_until IS NOT NULL;
`)
	return err
}

func (s *PostgresStore) Save(ctx context.Context, message Message) (Message, error) {
	if message.MessageID == "" {
		message.MessageID = NewMessageID()
	}
	now := time.Now()
	if message.Status == "" {
		message.Status = MessageStatusPending
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	if message.UpdatedAt.IsZero() {
		message.UpdatedAt = now
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO z_courier_downlink_messages (
  message_id, client_id, device_id, msg_id, body, ack_required, trace_id,
  session_id, status, attempts, next_retry_at, last_error, created_at,
  updated_at, sent_at, delivered_at, claim_owner, claim_until
) VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13,
  $14, $15, $16, $17, $18
)
ON CONFLICT (message_id) DO NOTHING
`,
		message.MessageID,
		message.ClientID,
		message.DeviceID,
		int64(message.MsgID),
		bytes.Clone(message.Body),
		message.AckRequired,
		message.TraceID,
		message.SessionID,
		string(message.Status),
		message.Attempts,
		nullTime(message.NextRetryAt),
		message.LastError,
		message.CreatedAt,
		message.UpdatedAt,
		nullTime(message.SentAt),
		nullTime(message.DeliveredAt),
		message.ClaimOwner,
		nullTime(message.ClaimUntil),
	)
	if err != nil {
		return Message{}, err
	}

	stored, ok, err := s.Get(ctx, message.MessageID)
	if err != nil {
		return Message{}, err
	}
	if !ok {
		return Message{}, ErrMessageNotFound
	}

	return stored, nil
}

func (s *PostgresStore) Get(ctx context.Context, messageID string) (Message, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE message_id = $1
`, messageID)

	message, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}

	return message, true, nil
}

func (s *PostgresStore) ListByStatus(ctx context.Context, status MessageStatus, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE status = $1
ORDER BY updated_at DESC, message_id ASC
LIMIT $2
`, string(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *PostgresStore) ListDuePending(ctx context.Context, now time.Time, limit int) ([]Message, error) {
	return s.ListDueRetry(ctx, now, 0, limit)
}

func (s *PostgresStore) ListDueRetry(ctx context.Context, now time.Time, ackTimeout time.Duration, limit int) ([]Message, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 100
	}
	includeAckTimeout := ackTimeout > 0
	ackDeadline := now.Add(-ackTimeout)

	rows, err := s.db.QueryContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE (
    (status = $1 AND (next_retry_at IS NULL OR next_retry_at <= $2))
    OR ($4 AND status = $5 AND ack_required = true AND sent_at IS NOT NULL AND sent_at <= $6)
  )
  AND (claim_until IS NULL OR claim_until <= $2)
ORDER BY created_at ASC, message_id ASC
LIMIT $3
`, string(MessageStatusPending), now, limit, includeAckTimeout, string(MessageStatusSent), ackDeadline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *PostgresStore) ClaimDuePending(ctx context.Context, now time.Time, limit int, owner string, lease time.Duration) ([]Message, error) {
	return s.ClaimDueRetry(ctx, now, 0, limit, owner, lease)
}

func (s *PostgresStore) ClaimDueRetry(ctx context.Context, now time.Time, ackTimeout time.Duration, limit int, owner string, lease time.Duration) ([]Message, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 100
	}
	if owner == "" {
		return s.ListDueRetry(ctx, now, ackTimeout, limit)
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	includeAckTimeout := ackTimeout > 0
	ackDeadline := now.Add(-ackTimeout)

	rows, err := s.db.QueryContext(ctx, `
WITH claimed AS (
  SELECT message_id
  FROM z_courier_downlink_messages
  WHERE (
      (status = $1 AND (next_retry_at IS NULL OR next_retry_at <= $2))
      OR ($7 AND status = $8 AND ack_required = true AND sent_at IS NOT NULL AND sent_at <= $9)
    )
    AND (claim_until IS NULL OR claim_until <= $2)
  ORDER BY created_at ASC, message_id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $3
)
UPDATE z_courier_downlink_messages AS m
SET claim_owner = $4,
    claim_until = $5,
    updated_at = $2
FROM claimed
WHERE m.message_id = claimed.message_id
RETURNING m.message_id, m.client_id, m.device_id, m.msg_id, m.body,
          m.ack_required, m.trace_id, m.session_id, m.status, m.attempts,
          m.next_retry_at, m.last_error, m.created_at, m.updated_at,
          m.sent_at, m.delivered_at, m.claim_owner, m.claim_until
`, string(MessageStatusPending), now, limit, owner, now.Add(lease), includeAckTimeout, string(MessageStatusSent), ackDeadline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *PostgresStore) ListPendingByClientDevice(ctx context.Context, clientID, deviceID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	now := time.Now()
	rows, err := s.db.QueryContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE status = $1
  AND client_id = $2
  AND device_id = $3
  AND (claim_until IS NULL OR claim_until <= $4)
ORDER BY created_at ASC, message_id ASC
LIMIT $5
`, string(MessageStatusPending), clientID, deviceID, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *PostgresStore) MarkSent(ctx context.Context, messageID, sessionID string, sentAt time.Time) error {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = CASE WHEN status IN ($5, $6) THEN status ELSE $2 END,
    session_id = CASE WHEN status IN ($5, $6) THEN session_id ELSE $3 END,
    attempts = CASE WHEN status IN ($5, $6) THEN attempts ELSE attempts + 1 END,
    last_error = CASE WHEN status IN ($5, $6) THEN last_error ELSE '' END,
    next_retry_at = CASE WHEN status IN ($5, $6) THEN next_retry_at ELSE NULL END,
    claim_owner = CASE WHEN status IN ($5, $6) THEN claim_owner ELSE '' END,
    claim_until = CASE WHEN status IN ($5, $6) THEN claim_until ELSE NULL END,
    sent_at = CASE WHEN status IN ($5, $6) THEN sent_at ELSE $4 END,
    updated_at = CASE WHEN status IN ($5, $6) THEN updated_at ELSE $4 END
WHERE message_id = $1
`, messageID, string(MessageStatusSent), sessionID, sentAt, string(MessageStatusDelivered), string(MessageStatusDiscarded))
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) MarkDelivered(ctx context.Context, messageID, clientID, deviceID string, deliveredAt time.Time) error {
	if deliveredAt.IsZero() {
		deliveredAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $4,
    last_error = '',
    next_retry_at = NULL,
    claim_owner = '',
    claim_until = NULL,
    delivered_at = $5,
    updated_at = $5
WHERE message_id = $1
  AND client_id = $2
  AND device_id = $3
`, messageID, clientID, deviceID, string(MessageStatusDelivered), deliveredAt)
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) MarkAttemptFailed(ctx context.Context, messageID, reason string, nextRetryAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = CASE WHEN status IN ($6, $7) THEN status ELSE $2 END,
    attempts = CASE WHEN status IN ($6, $7) THEN attempts ELSE attempts + 1 END,
    last_error = CASE WHEN status IN ($6, $7) THEN last_error ELSE $3 END,
    next_retry_at = CASE WHEN status IN ($6, $7) THEN next_retry_at ELSE $4 END,
    claim_owner = CASE WHEN status IN ($6, $7) THEN claim_owner ELSE '' END,
    claim_until = CASE WHEN status IN ($6, $7) THEN claim_until ELSE NULL END,
    updated_at = CASE WHEN status IN ($6, $7) THEN updated_at ELSE $5 END
WHERE message_id = $1
`, messageID, string(MessageStatusPending), reason, nullTime(nextRetryAt), time.Now(), string(MessageStatusDelivered), string(MessageStatusDiscarded))
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) MarkFailed(ctx context.Context, messageID, reason string, failedAt time.Time) error {
	if failedAt.IsZero() {
		failedAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = CASE WHEN status IN ($5, $6) THEN status ELSE $2 END,
    attempts = CASE WHEN status IN ($5, $6) THEN attempts ELSE attempts + 1 END,
    last_error = CASE WHEN status IN ($5, $6) THEN last_error ELSE $3 END,
    next_retry_at = CASE WHEN status IN ($5, $6) THEN next_retry_at ELSE NULL END,
    claim_owner = CASE WHEN status IN ($5, $6) THEN claim_owner ELSE '' END,
    claim_until = CASE WHEN status IN ($5, $6) THEN claim_until ELSE NULL END,
    updated_at = CASE WHEN status IN ($5, $6) THEN updated_at ELSE $4 END
WHERE message_id = $1
`, messageID, string(MessageStatusFailed), reason, failedAt, string(MessageStatusDelivered), string(MessageStatusDiscarded))
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) Requeue(ctx context.Context, messageID string, requeuedAt time.Time) error {
	if requeuedAt.IsZero() {
		requeuedAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $2,
    attempts = 0,
    last_error = '',
    next_retry_at = NULL,
    claim_owner = '',
    claim_until = NULL,
    session_id = '',
    sent_at = NULL,
    delivered_at = NULL,
    updated_at = $3
WHERE message_id = $1
`, messageID, string(MessageStatusPending), requeuedAt)
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) Discard(ctx context.Context, messageID, reason string, discardedAt time.Time) error {
	if discardedAt.IsZero() {
		discardedAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $2,
    last_error = $3,
    next_retry_at = NULL,
    claim_owner = '',
    claim_until = NULL,
    updated_at = $4
WHERE message_id = $1
`, messageID, string(MessageStatusDiscarded), reason, discardedAt)
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

type rowScanner interface {
	Scan(...any) error
}

func scanMessage(row rowScanner) (Message, error) {
	var message Message
	var msgID int64
	var status string
	var nextRetryAt sql.NullTime
	var sentAt sql.NullTime
	var deliveredAt sql.NullTime
	var claimUntil sql.NullTime

	if err := row.Scan(
		&message.MessageID,
		&message.ClientID,
		&message.DeviceID,
		&msgID,
		&message.Body,
		&message.AckRequired,
		&message.TraceID,
		&message.SessionID,
		&status,
		&message.Attempts,
		&nextRetryAt,
		&message.LastError,
		&message.CreatedAt,
		&message.UpdatedAt,
		&sentAt,
		&deliveredAt,
		&message.ClaimOwner,
		&claimUntil,
	); err != nil {
		return Message{}, err
	}

	message.MsgID = uint32(msgID)
	message.Status = MessageStatus(status)
	if nextRetryAt.Valid {
		message.NextRetryAt = nextRetryAt.Time
	}
	if sentAt.Valid {
		message.SentAt = sentAt.Time
	}
	if deliveredAt.Valid {
		message.DeliveredAt = deliveredAt.Time
	}
	if claimUntil.Valid {
		message.ClaimUntil = claimUntil.Time
	}
	message.Body = bytes.Clone(message.Body)

	return message, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	messages := make([]Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrMessageNotFound
	}

	return nil
}
