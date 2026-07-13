package downlink

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
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

const postgresDownlinkMigrationLockID int64 = 8_789_121_360_511_467

const postgresMessageColumns = `
message_id, client_id, device_id, msg_id, body, identity_fingerprint,
ack_required, trace_id, policy_name, policy_max_attempts, policy_max_age_ns,
policy_ack_timeout_ns, policy_retry_delay_ns, policy_backoff_multiplier,
policy_max_retry_delay_ns, policy_retry_jitter_ns,
session_id, status, attempts, next_retry_at, last_error, terminal_reason,
terminal_at, terminal_publish_status, terminal_publish_attempts,
terminal_next_publish_at, terminal_publish_error, terminal_published_at,
created_at, updated_at, sent_at, delivered_at, claim_owner, claim_until
`

const postgresTerminalColumns = `
event_id, message_id, client_id, device_id, msg_id, trace_id,
terminal_status, terminal_reason, policy_name, attempts,
message_created_at, terminal_at, gateway_node, publish_status,
publish_attempts, next_attempt_at, publish_last_error, published_at,
claim_owner, claim_until
`

const postgresInsertMessage = `
INSERT INTO z_courier_downlink_messages (
  message_id, client_id, device_id, msg_id, body, identity_fingerprint,
  ack_required, trace_id, policy_name, policy_max_attempts, policy_max_age_ns,
  policy_ack_timeout_ns, policy_retry_delay_ns, policy_backoff_multiplier,
  policy_max_retry_delay_ns, policy_retry_jitter_ns, session_id, status,
  attempts, next_retry_at, last_error, terminal_reason, terminal_at,
  terminal_publish_status, terminal_publish_attempts, terminal_next_publish_at,
  terminal_publish_error, terminal_published_at, created_at, updated_at,
  sent_at, delivered_at, claim_owner, claim_until
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, $22, $23,
  $24, $25, $26, $27, $28,
  $29, $30, $31, $32, $33, $34
)
ON CONFLICT (message_id) DO NOTHING
RETURNING ` + postgresMessageColumns

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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Serialize first-start migrations so concurrent gateway nodes do not race
	// in PostgreSQL system catalogs while creating this table and its indexes.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", postgresDownlinkMigrationLockID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS z_courier_downlink_messages (
  message_id TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  msg_id INTEGER NOT NULL,
  body BYTEA NOT NULL DEFAULT ''::bytea,
  identity_fingerprint BYTEA NOT NULL DEFAULT ''::bytea,
  ack_required BOOLEAN NOT NULL DEFAULT false,
  trace_id TEXT NOT NULL DEFAULT '',
  policy_name TEXT NOT NULL DEFAULT '',
  policy_max_attempts INTEGER NOT NULL DEFAULT 0,
  policy_max_age_ns BIGINT NOT NULL DEFAULT 0,
  policy_ack_timeout_ns BIGINT NOT NULL DEFAULT 0,
  policy_retry_delay_ns BIGINT NOT NULL DEFAULT 0,
  policy_backoff_multiplier DOUBLE PRECISION NOT NULL DEFAULT 0,
  policy_max_retry_delay_ns BIGINT NOT NULL DEFAULT 0,
  policy_retry_jitter_ns BIGINT NOT NULL DEFAULT 0,
  session_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  terminal_reason TEXT NOT NULL DEFAULT '',
  terminal_at TIMESTAMPTZ,
  terminal_publish_status TEXT NOT NULL DEFAULT 'disabled',
  terminal_publish_attempts INTEGER NOT NULL DEFAULT 0,
  terminal_next_publish_at TIMESTAMPTZ,
  terminal_publish_error TEXT NOT NULL DEFAULT '',
  terminal_published_at TIMESTAMPTZ,
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
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS identity_fingerprint BYTEA NOT NULL DEFAULT ''::bytea;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_name TEXT NOT NULL DEFAULT '';
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_max_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_max_age_ns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_ack_timeout_ns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_retry_delay_ns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_backoff_multiplier DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_max_retry_delay_ns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS policy_retry_jitter_ns BIGINT NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_at TIMESTAMPTZ;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_publish_status TEXT NOT NULL DEFAULT 'disabled';
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_publish_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_next_publish_at TIMESTAMPTZ;
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_publish_error TEXT NOT NULL DEFAULT '';
ALTER TABLE z_courier_downlink_messages
  ADD COLUMN IF NOT EXISTS terminal_published_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS z_courier_downlink_terminal_events (
  event_id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES z_courier_downlink_messages(message_id) ON DELETE CASCADE,
  client_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  msg_id INTEGER NOT NULL,
  trace_id TEXT NOT NULL DEFAULT '',
  terminal_status TEXT NOT NULL,
  terminal_reason TEXT NOT NULL,
  policy_name TEXT NOT NULL,
  attempts INTEGER NOT NULL,
  message_created_at TIMESTAMPTZ NOT NULL,
  terminal_at TIMESTAMPTZ NOT NULL,
  gateway_node TEXT NOT NULL DEFAULT '',
  publish_status TEXT NOT NULL DEFAULT 'pending',
  publish_attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ,
  publish_last_error TEXT NOT NULL DEFAULT '',
  published_at TIMESTAMPTZ,
  claim_owner TEXT NOT NULL DEFAULT '',
  claim_until TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS z_courier_downlink_terminal_events_message_status_idx
  ON z_courier_downlink_terminal_events (message_id, terminal_status);
CREATE INDEX IF NOT EXISTS z_courier_downlink_terminal_events_due_idx
  ON z_courier_downlink_terminal_events (publish_status, next_attempt_at, terminal_at)
  WHERE publish_status IN ('pending', 'failed');
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_client_device_status_idx
  ON z_courier_downlink_messages (client_id, device_id, status);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_status_idx
  ON z_courier_downlink_messages (status);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_next_retry_at_idx
  ON z_courier_downlink_messages (next_retry_at)
  WHERE next_retry_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_claim_until_idx
  ON z_courier_downlink_messages (claim_until)
  WHERE claim_until IS NOT NULL;
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_status_updated_at_idx
  ON z_courier_downlink_messages (status, updated_at);
CREATE INDEX IF NOT EXISTS z_courier_downlink_messages_status_updated_message_idx
  ON z_courier_downlink_messages (status, updated_at DESC, message_id ASC);
	`); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) Save(ctx context.Context, message Message) (SaveResult, error) {
	message = prepareMessageForSave(message, time.Now())

	inserted, err := insertPostgresMessage(ctx, s.db, message)
	if err == nil {
		return SaveResult{Message: inserted, Outcome: SaveOutcomeCreated}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SaveResult{}, err
	}

	stored, ok, err := s.Get(ctx, message.MessageID)
	if err != nil {
		return SaveResult{}, err
	}
	if !ok {
		return SaveResult{}, ErrMessageNotFound
	}
	if len(stored.IdentityFingerprint) != len(message.IdentityFingerprint) {
		stored.IdentityFingerprint = messageIdentityFingerprint(stored)
		if _, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET identity_fingerprint = $2
WHERE message_id = $1 AND octet_length(identity_fingerprint) = 0
`, stored.MessageID, bytes.Clone(stored.IdentityFingerprint)); err != nil {
			return SaveResult{}, err
		}
	}

	return saveResultForExisting(stored, message), nil
}

func (s *PostgresStore) SaveWithCapacity(
	ctx context.Context,
	message Message,
	capacity QueueCapacity,
) (SaveResult, error) {
	if !capacity.Enabled() {
		return s.Save(ctx, message)
	}
	message = prepareMessageForSave(message, time.Now())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockPostgresCapacityKey(ctx, tx, "message:"+message.MessageID); err != nil {
		return SaveResult{}, err
	}
	stored, err := scanMessage(tx.QueryRowContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE message_id = $1
FOR UPDATE
`, message.MessageID))
	if err == nil {
		if len(stored.IdentityFingerprint) != len(message.IdentityFingerprint) {
			stored.IdentityFingerprint = messageIdentityFingerprint(stored)
			if _, err := tx.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET identity_fingerprint = $2
WHERE message_id = $1 AND octet_length(identity_fingerprint) = 0
`, stored.MessageID, bytes.Clone(stored.IdentityFingerprint)); err != nil {
				return SaveResult{}, err
			}
		}
		result := saveResultForExisting(stored, message)
		if err := tx.Commit(); err != nil {
			return SaveResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SaveResult{}, err
	}

	if message.Status == MessageStatusPending {
		if err := checkPostgresQueueCapacity(ctx, tx, message.ClientID, message.DeviceID, capacity); err != nil {
			return SaveResult{}, err
		}
	}
	inserted, err := insertPostgresMessage(ctx, tx, message)
	if err != nil {
		return SaveResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{Message: inserted, Outcome: SaveOutcomeCreated}, nil
}

func prepareMessageForSave(message Message, now time.Time) Message {
	if message.MessageID == "" {
		message.MessageID = NewMessageID()
	}
	if message.Status == "" {
		message.Status = MessageStatusPending
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	if message.UpdatedAt.IsZero() {
		message.UpdatedAt = now
	}
	message.IdentityFingerprint = messageIdentityFingerprint(message)
	return message
}

type postgresMessageQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertPostgresMessage(ctx context.Context, queryer postgresMessageQueryer, message Message) (Message, error) {
	return scanMessage(queryer.QueryRowContext(ctx, postgresInsertMessage,
		message.MessageID,
		message.ClientID,
		message.DeviceID,
		int64(message.MsgID),
		bytes.Clone(message.Body),
		bytes.Clone(message.IdentityFingerprint),
		message.AckRequired,
		message.TraceID,
		message.Policy.Name,
		message.Policy.MaxAttempts,
		int64(message.Policy.MaxAge),
		int64(message.Policy.AckTimeout),
		int64(message.Policy.InitialRetryDelay),
		message.Policy.BackoffMultiplier,
		int64(message.Policy.MaxRetryDelay),
		int64(message.Policy.RetryJitter),
		message.SessionID,
		string(message.Status),
		message.Attempts,
		nullTime(message.NextRetryAt),
		message.LastError,
		message.TerminalReason,
		nullTime(message.TerminalAt),
		terminalPublicationStatusValue(message.TerminalPublishStatus),
		message.TerminalPublishAttempts,
		nullTime(message.TerminalNextPublishAt),
		message.TerminalPublishError,
		nullTime(message.TerminalPublishedAt),
		message.CreatedAt,
		message.UpdatedAt,
		nullTime(message.SentAt),
		nullTime(message.DeliveredAt),
		message.ClaimOwner,
		nullTime(message.ClaimUntil),
	))
}

func saveResultForExisting(stored, incoming Message) SaveResult {
	outcome := SaveOutcomeExisting
	if !messagesHaveSameIdentity(stored, incoming) {
		outcome = SaveOutcomeConflict
	}
	return SaveResult{Message: stored, Outcome: outcome}
}

func checkPostgresQueueCapacity(
	ctx context.Context,
	tx *sql.Tx,
	clientID string,
	deviceID string,
	capacity QueueCapacity,
) error {
	if capacity.MaxPendingGlobal > 0 {
		if err := lockPostgresCapacityKey(ctx, tx, "global"); err != nil {
			return err
		}
		pending, err := countPostgresPending(ctx, tx, "", "")
		if err != nil {
			return err
		}
		if pending >= capacity.MaxPendingGlobal {
			return newQueueCapacityError(QueueCapacityScopeGlobal, pending, capacity.MaxPendingGlobal)
		}
	}
	if capacity.MaxPendingPerDevice > 0 {
		if err := lockPostgresCapacityKey(ctx, tx, "device:"+clientID+"\x00"+deviceID); err != nil {
			return err
		}
		pending, err := countPostgresPending(ctx, tx, clientID, deviceID)
		if err != nil {
			return err
		}
		if pending >= capacity.MaxPendingPerDevice {
			return newQueueCapacityError(QueueCapacityScopeDevice, pending, capacity.MaxPendingPerDevice)
		}
	}
	return nil
}

func countPostgresPending(ctx context.Context, tx *sql.Tx, clientID, deviceID string) (int, error) {
	var pending int
	if clientID == "" && deviceID == "" {
		err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE status = $1
`, string(MessageStatusPending)).Scan(&pending)
		return pending, err
	}
	err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE status = $1 AND client_id = $2 AND device_id = $3
`, string(MessageStatusPending), clientID, deviceID).Scan(&pending)
	return pending, err
}

func lockPostgresCapacityKey(ctx context.Context, tx *sql.Tx, value string) error {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte("z-courier-downlink-capacity-v1:"))
	_, _ = digest.Write([]byte(value))
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(digest.Sum64()))
	return err
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
	result, err := s.ListByStatusPage(ctx, MessageListQuery{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (s *PostgresStore) ListByStatusPage(ctx context.Context, query MessageListQuery) (MessageListResult, error) {
	query = normalizeMessageListQuery(query)
	result := MessageListResult{
		Status:   query.Status,
		Limit:    query.Limit,
		Cursor:   query.Cursor,
		Messages: make([]Message, 0, query.Limit),
	}
	if err := s.db.QueryRowContext(ctx, `
	SELECT COUNT(*)
	FROM z_courier_downlink_messages
	WHERE status = $1
	`, string(query.Status)).Scan(&result.Total); err != nil {
		return MessageListResult{}, err
	}

	where := "WHERE status = $1"
	args := []any{string(query.Status)}
	if !messageListCursorIsZero(query.Cursor) {
		args = append(args, query.Cursor.UpdatedAt, query.Cursor.MessageID)
		where += fmt.Sprintf(" AND (updated_at < $%d OR (updated_at = $%d AND message_id > $%d))", len(args)-1, len(args)-1, len(args))
	}
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, `
	SELECT `+postgresMessageColumns+`
	FROM z_courier_downlink_messages
	`+where+`
	ORDER BY updated_at DESC, message_id ASC
	LIMIT $`+fmt.Sprint(len(args))+`
	`, args...)
	if err != nil {
		return MessageListResult{}, err
	}
	defer rows.Close()

	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return MessageListResult{}, err
		}
		if len(result.Messages) >= query.Limit {
			result.HasMore = true
			break
		}
		result.Messages = append(result.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return MessageListResult{}, err
	}
	if result.HasMore && len(result.Messages) > 0 {
		result.NextCursor = messageListCursorFromMessage(result.Messages[len(result.Messages)-1])
	}
	return result, nil
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
	return s.listDueRetry(ctx, now, ackTimeout, limit)
}

func (s *PostgresStore) ListDueRetryFair(
	ctx context.Context,
	now time.Time,
	ackTimeout time.Duration,
	limit int,
	candidateLimit int,
) (RetrySelection, error) {
	if now.IsZero() {
		now = time.Now()
	}
	limit, candidateLimit = normalizeRetrySelectionLimits(limit, candidateLimit)
	candidates, err := s.listDueRetry(ctx, now, ackTimeout, candidateLimit)
	if err != nil {
		return RetrySelection{}, err
	}
	return fairRetrySelection(candidates, limit), nil
}

func (s *PostgresStore) listDueRetry(
	ctx context.Context,
	now time.Time,
	ackTimeout time.Duration,
	limit int,
) ([]Message, error) {
	includeAckTimeout := ackTimeout > 0
	ackDeadline := now.Add(-ackTimeout)

	rows, err := s.db.QueryContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE (
    (status = $1 AND (next_retry_at IS NULL OR next_retry_at <= $2))
    OR (status = $5 AND ack_required = true AND (
      (next_retry_at IS NOT NULL AND next_retry_at <= $2)
      OR (next_retry_at IS NULL AND $4 AND sent_at IS NOT NULL AND sent_at <= $6)
    ))
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
      OR (status = $7 AND ack_required = true AND (
        (next_retry_at IS NOT NULL AND next_retry_at <= $2)
        OR (next_retry_at IS NULL AND $6 AND sent_at IS NOT NULL AND sent_at <= $8)
      ))
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
          m.identity_fingerprint, m.ack_required, m.trace_id, m.policy_name,
          m.policy_max_attempts, m.policy_max_age_ns, m.policy_ack_timeout_ns,
          m.policy_retry_delay_ns, m.policy_backoff_multiplier,
          m.policy_max_retry_delay_ns, m.policy_retry_jitter_ns, m.session_id,
          m.status, m.attempts, m.next_retry_at, m.last_error,
          m.terminal_reason, m.terminal_at, m.terminal_publish_status,
          m.terminal_publish_attempts, m.terminal_next_publish_at,
          m.terminal_publish_error, m.terminal_published_at, m.created_at,
          m.updated_at, m.sent_at, m.delivered_at, m.claim_owner, m.claim_until
`, string(MessageStatusPending), now, limit, owner, now.Add(lease), includeAckTimeout, string(MessageStatusSent), ackDeadline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *PostgresStore) ClaimDueRetryFair(
	ctx context.Context,
	now time.Time,
	ackTimeout time.Duration,
	limit int,
	candidateLimit int,
	owner string,
	lease time.Duration,
) (RetrySelection, error) {
	if now.IsZero() {
		now = time.Now()
	}
	limit, candidateLimit = normalizeRetrySelectionLimits(limit, candidateLimit)
	if owner == "" {
		return s.ListDueRetryFair(ctx, now, ackTimeout, limit, candidateLimit)
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	includeAckTimeout := ackTimeout > 0
	ackDeadline := now.Add(-ackTimeout)

	rows, err := s.db.QueryContext(ctx, `
WITH candidates AS MATERIALIZED (
  SELECT message_id, client_id, device_id, created_at
  FROM z_courier_downlink_messages
  WHERE (
      (status = $1 AND (next_retry_at IS NULL OR next_retry_at <= $2))
      OR (status = $7 AND ack_required = true AND (
        (next_retry_at IS NOT NULL AND next_retry_at <= $2)
        OR (next_retry_at IS NULL AND $6 AND sent_at IS NOT NULL AND sent_at <= $8)
      ))
    )
    AND (claim_until IS NULL OR claim_until <= $2)
  ORDER BY created_at ASC, message_id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $3
), ranked AS (
  SELECT message_id, created_at,
         ROW_NUMBER() OVER (
           PARTITION BY client_id, device_id
           ORDER BY created_at ASC, message_id ASC
         ) AS device_position
  FROM candidates
), selected AS (
  SELECT message_id
  FROM ranked
  ORDER BY device_position ASC, created_at ASC, message_id ASC
  LIMIT $9
)
UPDATE z_courier_downlink_messages AS m
SET claim_owner = $4,
    claim_until = $5,
    updated_at = $2
FROM selected
WHERE m.message_id = selected.message_id
RETURNING m.message_id, m.client_id, m.device_id, m.msg_id, m.body,
          m.identity_fingerprint, m.ack_required, m.trace_id, m.policy_name,
          m.policy_max_attempts, m.policy_max_age_ns, m.policy_ack_timeout_ns,
          m.policy_retry_delay_ns, m.policy_backoff_multiplier,
          m.policy_max_retry_delay_ns, m.policy_retry_jitter_ns, m.session_id,
          m.status, m.attempts, m.next_retry_at, m.last_error,
          m.terminal_reason, m.terminal_at, m.terminal_publish_status,
          m.terminal_publish_attempts, m.terminal_next_publish_at,
          m.terminal_publish_error, m.terminal_published_at, m.created_at,
          m.updated_at, m.sent_at, m.delivered_at, m.claim_owner, m.claim_until
`,
		string(MessageStatusPending),
		now,
		candidateLimit,
		owner,
		now.Add(lease),
		includeAckTimeout,
		string(MessageStatusSent),
		ackDeadline,
		limit,
	)
	if err != nil {
		return RetrySelection{}, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return RetrySelection{}, err
	}
	return retrySelectionFromMessages(messages, RetrySelectionModeFair), nil
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

func (s *PostgresStore) MarkSent(ctx context.Context, messageID, sessionID string, sentAt, nextRetryAt time.Time) error {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = CASE WHEN status IN ($6, $7) THEN status ELSE $2 END,
    session_id = CASE WHEN status IN ($6, $7) THEN session_id ELSE $3 END,
    attempts = CASE WHEN status IN ($6, $7) THEN attempts ELSE attempts + 1 END,
    last_error = CASE WHEN status IN ($6, $7) THEN last_error ELSE '' END,
    next_retry_at = CASE WHEN status IN ($6, $7) THEN next_retry_at ELSE $5 END,
    claim_owner = CASE WHEN status IN ($6, $7) THEN claim_owner ELSE '' END,
    claim_until = CASE WHEN status IN ($6, $7) THEN claim_until ELSE NULL END,
    sent_at = CASE WHEN status IN ($6, $7) THEN sent_at ELSE $4 END,
    updated_at = CASE WHEN status IN ($6, $7) THEN updated_at ELSE $4 END
WHERE message_id = $1
`, messageID, string(MessageStatusSent), sessionID, sentAt, nullTime(nextRetryAt), string(MessageStatusDelivered), string(MessageStatusDiscarded))
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

func (s *PostgresStore) MarkFailed(ctx context.Context, messageID string, transition TerminalTransition) error {
	return s.transitionTerminal(ctx, messageID, MessageStatusFailed, transition.Reason, transition, true)
}

func (s *PostgresStore) Requeue(ctx context.Context, messageID string, requeuedAt time.Time) error {
	return requeuePostgresMessage(ctx, s.db, messageID, requeuedAt)
}

func (s *PostgresStore) RequeueWithCapacity(
	ctx context.Context,
	messageID string,
	requeuedAt time.Time,
	capacity QueueCapacity,
) error {
	if !capacity.Enabled() {
		return s.Requeue(ctx, messageID, requeuedAt)
	}
	if requeuedAt.IsZero() {
		requeuedAt = time.Now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockPostgresCapacityKey(ctx, tx, "message:"+messageID); err != nil {
		return err
	}
	message, err := scanMessage(tx.QueryRowContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE message_id = $1
FOR UPDATE
`, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if message.Status == MessageStatusDelivered || message.Status == MessageStatusDiscarded {
		return ErrInvalidTransition
	}
	if message.Status != MessageStatusPending {
		if err := checkPostgresQueueCapacity(ctx, tx, message.ClientID, message.DeviceID, capacity); err != nil {
			return err
		}
	}
	if err := requeuePostgresMessage(ctx, tx, messageID, requeuedAt); err != nil {
		return err
	}
	return tx.Commit()
}

type postgresMessageExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func requeuePostgresMessage(
	ctx context.Context,
	execer postgresMessageExecer,
	messageID string,
	requeuedAt time.Time,
) error {
	if requeuedAt.IsZero() {
		requeuedAt = time.Now()
	}

	result, err := execer.ExecContext(ctx, `
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
    terminal_reason = '',
    terminal_at = NULL,
    terminal_publish_status = 'disabled',
    terminal_publish_attempts = 0,
    terminal_next_publish_at = NULL,
    terminal_publish_error = '',
    terminal_published_at = NULL,
    updated_at = $3
WHERE message_id = $1
`, messageID, string(MessageStatusPending), requeuedAt)
	if err != nil {
		return err
	}

	return requireAffected(result)
}

func (s *PostgresStore) Discard(ctx context.Context, messageID, reason string, transition TerminalTransition) error {
	if transition.Reason == "" {
		transition.Reason = TerminalReasonOperatorDiscard
	}
	return s.transitionTerminal(ctx, messageID, MessageStatusDiscarded, reason, transition, false)
}

func (s *PostgresStore) transitionTerminal(
	ctx context.Context,
	messageID string,
	status MessageStatus,
	lastError string,
	transition TerminalTransition,
	protectExistingTerminal bool,
) error {
	if transition.At.IsZero() {
		transition.At = time.Now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	message, err := scanMessage(tx.QueryRowContext(ctx, `
SELECT `+postgresMessageColumns+`
FROM z_courier_downlink_messages
WHERE message_id = $1
FOR UPDATE
`, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if protectExistingTerminal && (message.Status == MessageStatusDelivered || message.Status == MessageStatusDiscarded) {
		return tx.Commit()
	}

	firstTransition := message.Status != status || message.TerminalReason == ""
	message.Status = status
	if transition.Attempted {
		message.Attempts++
	}
	message.LastError = lastError
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.UpdatedAt = transition.At
	if firstTransition {
		message.TerminalReason = transition.Reason
		message.TerminalAt = transition.At
		message.TerminalPublishStatus = terminalPublicationStatus(transition.Publish)
		message.TerminalPublishAttempts = 0
		message.TerminalNextPublishAt = time.Time{}
		message.TerminalPublishError = ""
		message.TerminalPublishedAt = time.Time{}
	}
	if firstTransition && transition.Publish {
		event := newTerminalEvent(message, status, transition)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO z_courier_downlink_terminal_events (
  event_id, message_id, client_id, device_id, msg_id, trace_id,
  terminal_status, terminal_reason, policy_name, attempts,
  message_created_at, terminal_at, gateway_node, publish_status, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10,
  $11, $12, $13, $14, $15
)
ON CONFLICT (event_id) DO NOTHING
`,
			event.EventID,
			event.MessageID,
			event.ClientID,
			event.DeviceID,
			int64(event.MsgID),
			event.TraceID,
			string(event.TerminalStatus),
			event.TerminalReason,
			event.PolicyName,
			event.Attempts,
			event.MessageCreated,
			event.TerminalAt,
			event.GatewayNode,
			string(TerminalPublicationPending),
			event.TerminalAt,
		); err != nil {
			return err
		}
		record, err := scanTerminalRecord(tx.QueryRowContext(ctx, `
SELECT `+postgresTerminalColumns+`
FROM z_courier_downlink_terminal_events
WHERE event_id = $1
`, event.EventID))
		if err != nil {
			return err
		}
		applyTerminalRecord(&message, record)
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET status = $2,
    attempts = $3,
    last_error = $4,
    next_retry_at = NULL,
    claim_owner = '',
    claim_until = NULL,
    terminal_reason = $5,
    terminal_at = $6,
    terminal_publish_status = $7,
    terminal_publish_attempts = $8,
    terminal_next_publish_at = $9,
    terminal_publish_error = $10,
    terminal_published_at = $11,
    updated_at = $12
WHERE message_id = $1
`,
		messageID,
		string(message.Status),
		message.Attempts,
		message.LastError,
		message.TerminalReason,
		nullTime(message.TerminalAt),
		terminalPublicationStatusValue(message.TerminalPublishStatus),
		message.TerminalPublishAttempts,
		nullTime(message.TerminalNextPublishAt),
		message.TerminalPublishError,
		nullTime(message.TerminalPublishedAt),
		message.UpdatedAt,
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) ClaimDueTerminal(
	ctx context.Context,
	now time.Time,
	limit int,
	owner string,
	lease time.Duration,
) ([]TerminalRecord, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 100
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}

	rows, err := s.db.QueryContext(ctx, `
WITH claimed AS (
  SELECT event_id
  FROM z_courier_downlink_terminal_events
  WHERE publish_status IN ($1, $2)
    AND (next_attempt_at IS NULL OR next_attempt_at <= $3)
    AND (claim_until IS NULL OR claim_until <= $3)
  ORDER BY terminal_at ASC, event_id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $4
)
UPDATE z_courier_downlink_terminal_events AS event
SET claim_owner = $5,
    claim_until = $6,
    updated_at = $3
FROM claimed
WHERE event.event_id = claimed.event_id
RETURNING
  event.event_id, event.message_id, event.client_id, event.device_id,
  event.msg_id, event.trace_id, event.terminal_status, event.terminal_reason,
  event.policy_name, event.attempts, event.message_created_at,
  event.terminal_at, event.gateway_node, event.publish_status,
  event.publish_attempts, event.next_attempt_at, event.publish_last_error,
  event.published_at, event.claim_owner, event.claim_until
`,
		string(TerminalPublicationPending),
		string(TerminalPublicationFailed),
		now,
		limit,
		owner,
		now.Add(lease),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]TerminalRecord, 0)
	for rows.Next() {
		record, err := scanTerminalRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *PostgresStore) MarkTerminalPublished(
	ctx context.Context,
	messageID string,
	status MessageStatus,
	publishedAt time.Time,
) error {
	if publishedAt.IsZero() {
		publishedAt = time.Now()
	}
	tx, err := s.beginTerminalEventUpdate(ctx, messageID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var found bool
	err = tx.QueryRowContext(ctx, `
WITH published AS (
  UPDATE z_courier_downlink_terminal_events
  SET publish_status = $3,
      publish_attempts = publish_attempts + 1,
      next_attempt_at = NULL,
      publish_last_error = '',
      published_at = $4,
      claim_owner = '',
      claim_until = NULL,
      updated_at = $4
  WHERE message_id = $1
    AND terminal_status = $2
    AND publish_status <> $3
  RETURNING publish_attempts, published_at
), message_updated AS (
  UPDATE z_courier_downlink_messages AS message
  SET terminal_publish_status = $3,
      terminal_publish_attempts = published.publish_attempts,
      terminal_next_publish_at = NULL,
      terminal_publish_error = '',
      terminal_published_at = published.published_at
  FROM published
  WHERE message.message_id = $1 AND message.status = $2
)
SELECT EXISTS(
  SELECT 1
  FROM z_courier_downlink_terminal_events
  WHERE message_id = $1 AND terminal_status = $2
)
`, messageID, string(status), string(TerminalPublicationPublished), publishedAt).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return ErrMessageNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) MarkTerminalPublishFailed(
	ctx context.Context,
	messageID string,
	status MessageStatus,
	reason string,
	nextAttemptAt time.Time,
) error {
	now := time.Now()
	tx, err := s.beginTerminalEventUpdate(ctx, messageID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var found bool
	err = tx.QueryRowContext(ctx, `
WITH failed AS (
  UPDATE z_courier_downlink_terminal_events
  SET publish_status = $3,
      publish_attempts = publish_attempts + 1,
      next_attempt_at = $4,
      publish_last_error = $5,
      claim_owner = '',
      claim_until = NULL,
      updated_at = $6
  WHERE message_id = $1
    AND terminal_status = $2
    AND publish_status <> $7
  RETURNING publish_attempts, next_attempt_at, publish_last_error
), message_updated AS (
  UPDATE z_courier_downlink_messages AS message
  SET terminal_publish_status = $3,
      terminal_publish_attempts = failed.publish_attempts,
      terminal_next_publish_at = failed.next_attempt_at,
      terminal_publish_error = failed.publish_last_error
  FROM failed
  WHERE message.message_id = $1 AND message.status = $2
)
SELECT EXISTS(
  SELECT 1
  FROM z_courier_downlink_terminal_events
  WHERE message_id = $1 AND terminal_status = $2
)
`, messageID, string(status), string(TerminalPublicationFailed), nullTime(nextAttemptAt), reason, now, string(TerminalPublicationPublished)).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return ErrMessageNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) beginTerminalEventUpdate(ctx context.Context, messageID string) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `
SELECT status
FROM z_courier_downlink_messages
WHERE message_id = $1
FOR UPDATE
`, messageID).Scan(&status); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMessageNotFound
		}
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) DeleteExpired(ctx context.Context, status MessageStatus, before time.Time, limit int) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	if limit <= 0 {
		limit = 1000
	}

	result, err := s.db.ExecContext(ctx, `
WITH expired AS (
  SELECT message_id
  FROM z_courier_downlink_messages
  WHERE status = $1
    AND updated_at < $2
    AND NOT EXISTS (
      SELECT 1
      FROM z_courier_downlink_terminal_events AS event
      WHERE event.message_id = z_courier_downlink_messages.message_id
        AND event.publish_status IN ('pending', 'failed')
    )
  ORDER BY updated_at ASC, message_id ASC
  LIMIT $3
)
DELETE FROM z_courier_downlink_messages AS m
USING expired
WHERE m.message_id = expired.message_id
`, string(status), before, limit)
	if err != nil {
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

type rowScanner interface {
	Scan(...any) error
}

func scanTerminalRecord(row rowScanner) (TerminalRecord, error) {
	var record TerminalRecord
	var msgID int64
	var terminalStatus string
	var publishStatus string
	var nextAttemptAt sql.NullTime
	var publishedAt sql.NullTime
	var claimUntil sql.NullTime

	if err := row.Scan(
		&record.Event.EventID,
		&record.Event.MessageID,
		&record.Event.ClientID,
		&record.Event.DeviceID,
		&msgID,
		&record.Event.TraceID,
		&terminalStatus,
		&record.Event.TerminalReason,
		&record.Event.PolicyName,
		&record.Event.Attempts,
		&record.Event.MessageCreated,
		&record.Event.TerminalAt,
		&record.Event.GatewayNode,
		&publishStatus,
		&record.PublishAttempts,
		&nextAttemptAt,
		&record.LastError,
		&publishedAt,
		&record.ClaimOwner,
		&claimUntil,
	); err != nil {
		return TerminalRecord{}, err
	}

	record.Event.Version = TerminalEventVersion
	record.Event.Type = TerminalEventType
	record.Event.MsgID = uint32(msgID)
	record.Event.TerminalStatus = MessageStatus(terminalStatus)
	record.Status = TerminalPublicationStatus(publishStatus)
	if nextAttemptAt.Valid {
		record.NextAttemptAt = nextAttemptAt.Time
	}
	if publishedAt.Valid {
		record.PublishedAt = publishedAt.Time
	}
	if claimUntil.Valid {
		record.ClaimUntil = claimUntil.Time
	}
	return record, nil
}

func scanMessage(row rowScanner) (Message, error) {
	var message Message
	var msgID int64
	var status string
	var nextRetryAt sql.NullTime
	var sentAt sql.NullTime
	var deliveredAt sql.NullTime
	var claimUntil sql.NullTime
	var terminalAt sql.NullTime
	var terminalNextPublishAt sql.NullTime
	var terminalPublishedAt sql.NullTime
	var terminalPublishStatus string
	var policyMaxAge int64
	var policyAckTimeout int64
	var policyRetryDelay int64
	var policyMaxRetryDelay int64
	var policyRetryJitter int64

	if err := row.Scan(
		&message.MessageID,
		&message.ClientID,
		&message.DeviceID,
		&msgID,
		&message.Body,
		&message.IdentityFingerprint,
		&message.AckRequired,
		&message.TraceID,
		&message.Policy.Name,
		&message.Policy.MaxAttempts,
		&policyMaxAge,
		&policyAckTimeout,
		&policyRetryDelay,
		&message.Policy.BackoffMultiplier,
		&policyMaxRetryDelay,
		&policyRetryJitter,
		&message.SessionID,
		&status,
		&message.Attempts,
		&nextRetryAt,
		&message.LastError,
		&message.TerminalReason,
		&terminalAt,
		&terminalPublishStatus,
		&message.TerminalPublishAttempts,
		&terminalNextPublishAt,
		&message.TerminalPublishError,
		&terminalPublishedAt,
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
	message.TerminalPublishStatus = TerminalPublicationStatus(terminalPublishStatus)
	message.Policy.MaxAge = time.Duration(policyMaxAge)
	message.Policy.AckTimeout = time.Duration(policyAckTimeout)
	message.Policy.InitialRetryDelay = time.Duration(policyRetryDelay)
	message.Policy.MaxRetryDelay = time.Duration(policyMaxRetryDelay)
	message.Policy.RetryJitter = time.Duration(policyRetryJitter)
	if nextRetryAt.Valid {
		message.NextRetryAt = nextRetryAt.Time
	}
	if terminalAt.Valid {
		message.TerminalAt = terminalAt.Time
	}
	if terminalNextPublishAt.Valid {
		message.TerminalNextPublishAt = terminalNextPublishAt.Time
	}
	if terminalPublishedAt.Valid {
		message.TerminalPublishedAt = terminalPublishedAt.Time
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
	message.IdentityFingerprint = bytes.Clone(message.IdentityFingerprint)

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

func terminalPublicationStatusValue(status TerminalPublicationStatus) string {
	if status == "" {
		return string(TerminalPublicationDisabled)
	}
	return string(status)
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
