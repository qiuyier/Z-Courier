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
  delivered_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_client_device_status_idx
  ON z_courier_downlink_messages (client_id, device_id, status);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_next_retry_at_idx
  ON z_courier_downlink_messages (next_retry_at)
  WHERE next_retry_at IS NOT NULL;
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
  updated_at, sent_at, delivered_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13,
  $14, $15, $16
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
SELECT message_id, client_id, device_id, msg_id, body, ack_required, trace_id,
       session_id, status, attempts, next_retry_at, last_error, created_at,
       updated_at, sent_at, delivered_at
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

func (s *PostgresStore) MarkSent(ctx context.Context, messageID, sessionID string, sentAt time.Time) error {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $2,
    session_id = $3,
    attempts = attempts + 1,
    last_error = '',
    next_retry_at = NULL,
    sent_at = $4,
    updated_at = $4
WHERE message_id = $1
`, messageID, string(MessageStatusSent), sessionID, sentAt)
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) MarkAttemptFailed(ctx context.Context, messageID, reason string, nextRetryAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $2,
    attempts = attempts + 1,
    last_error = $3,
    next_retry_at = $4,
    updated_at = $5
WHERE message_id = $1
`, messageID, string(MessageStatusPending), reason, nullTime(nextRetryAt), time.Now())
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
	message.Body = bytes.Clone(message.Body)

	return message, nil
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
