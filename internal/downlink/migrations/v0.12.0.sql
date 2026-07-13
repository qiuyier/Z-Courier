-- Authoritative PostgreSQL schema for the v0.12.0 reliable-downlink store.
-- The gateway runs this file inside a transaction protected by an advisory
-- lock. Operators running it manually should use psql --single-transaction.

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
