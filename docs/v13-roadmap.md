# V13 Roadmap

V13 is the planning track for the next public milestone after `v0.12.0`. Its
target SemVer version is `v0.13.0`, not `v13.0.0`.

`v0.12.0` made terminal delivery outcomes durable and observable: a terminal
message creates a body-free PostgreSQL outbox event, and the gateway can retry
publication to NSQ without changing the original message state. V13 makes that
same outcome usable by deployments that do not operate NSQ.

This document is a roadmap, not a release guarantee. A feature is not part of
`v0.13.0` until it is implemented, documented, tested, and included in the
release guide for that version.

## Product Direction

The V13 scope is deliberately narrow: add a signed HTTP webhook transport for
the existing terminal-event outbox. It must not create a second retry queue,
alter client delivery behavior, or expose opaque business message bodies.

The guiding rule is: transport can vary, event identity and reliability state
must not. The existing terminal event remains the canonical body-free envelope;
the HTTP adapter only serializes and sends it.

V13 should focus on:

- Adding an opt-in `http` terminal publisher alongside `none` and `nsq`.
- Reusing the PostgreSQL outbox, publication claim, retry, jitter, backoff, and
  published/failed states already used by NSQ terminal publication.
- Signing every webhook request with the existing replay-resistant Z-Courier
  HMAC protocol so a receiver can verify its gateway origin.
- Giving receivers a stable idempotency key and an unambiguous success/failure
  contract.
- Extending configuration validation, metrics, diagnostics, the console, E2E,
  Compose, Helm, and Chinese/English operations documentation.

## Non-Goals

V13 does not target:

- Exactly-once webhook delivery. Webhook delivery remains at-least-once.
- Including the arbitrary message `Body`, tokens, HMAC secrets, DSNs, or other
  credentials in terminal events.
- Per-message templates, transformations, routing rules, or arbitrary caller
  controlled headers.
- Receiving webhooks, acknowledging business processing, or coordinating a
  transaction with a receiver.
- A generic HTTP upstream adapter rewrite, Kafka/NATS adapters, or a public
  event-broker service.
- Changing the client packet protocol or existing Go/PHP SDK wire behavior.

## Webhook Contract

The adapter sends one JSON `POST` request per claimed terminal outbox record.
The JSON body is the existing `downlink.TerminalEvent` envelope, including:

- `version: 1` and `type: z_courier.downlink.terminal`;
- stable `event_id`, currently `<message_id>:<terminal_status>`;
- message/client/device routing metadata, terminal reason, policy, attempts,
  timestamps, and gateway node;
- no business `body` field.

The request is successful only for a `2xx` response. Redirects are not
followed. Transport errors, timeouts, and non-`2xx` responses leave the event
in the existing terminal-publication retry path. The stored error is sanitized
and bounded exactly as for the current publisher.

Every request uses the existing `ZCOURIER-HMAC-SHA256` request-signing format:
`X-ZCourier-Key-ID`, `X-ZCourier-Timestamp`, `X-ZCourier-Nonce`, and
`X-ZCourier-Signature`. The signature covers method, escaped path, canonical
query, timestamp, nonce, and the exact JSON bytes sent. A receiver can reuse
the public Go signing verifier or implement the documented canonical format in
another language.

Receivers must de-duplicate at least by `event_id`. A temporary receiver
failure can produce another signed request for the same event; this is normal
at-least-once behavior.

## Configuration Shape

The target configuration is intentionally explicit:

```yaml
downlink:
  terminal:
    publisher:
      type: http
      http:
        url: https://terminal-events.example.internal/v1/z-courier
        timeout: 5s
        hmac:
          key_id: gateway-terminal-v1
          secret: ${ZCOURIER_TERMINAL_WEBHOOK_HMAC_SECRET}
```

`https` is the production default. An insecure `http` endpoint, if supported
for local development, must require an explicit opt-in and emit a validation
warning. A publisher still requires PostgreSQL terminal-outbox storage; memory
storage cannot provide crash-safe or cross-node publication claims.

## Workstreams

### V13.1 Contract And Configuration

Purpose: make the HTTP endpoint and trust boundary deterministic before any
traffic is emitted.

- Add `http` as a validated terminal publisher type.
- Define URL, timeout, HMAC key ID, and secret requirements.
- Reject ambiguous configuration such as HTTP settings with `nsq`/`none`, empty
  secrets, invalid URLs, unsupported schemes, or a publisher without durable
  PostgreSQL storage.
- Document the JSON envelope, signature headers, receiver verification, `2xx`
  success rule, and receiver de-duplication contract.

Acceptance criteria:

- Existing `none` and `nsq` configuration stays compatible.
- Invalid or insecure production-like configuration fails clearly or produces
  an intentional warning.
- Secrets never appear in configuration errors, metrics, logs, diagnostics, or
  the admin console.

### V13.2 Signed HTTP Publisher

Purpose: connect the existing durable outbox to a secure HTTP receiver.

- Implement a small outbound HTTP adapter behind the existing
  `TerminalPublisher` interface.
- Reuse `pkg/sdk/signing.Signer`; do not invent a second signature format.
- Set JSON content type and sign the exact bytes written to the request.
- Bound request time with the configured timeout and disable automatic
  redirects.
- Treat only `2xx` as publication success; return sanitized transport and
  status errors to the existing terminal worker.
- Ensure cancellation and gateway shutdown release resources promptly.

Acceptance criteria:

- A verifying test receiver accepts the signed envelope and observes no
  business body.
- Bad signatures, timeout, network failure, and non-`2xx` responses schedule
  the existing retry flow without changing terminal delivery state.
- A retry sends the same `event_id` and payload semantics.

### V13.3 Operations And Deployment Parity

Purpose: make webhook publication safe to operate in single-node and clustered
deployments.

- Extend terminal publication metrics, alerts, diagnostics, and console status
  with publisher type and recent sanitized failure context where useful.
- Add a deterministic local webhook receiver to single-node and two-node E2E.
- Verify shared PostgreSQL claims publish one successful event across two
  gateways while a failed receiver is retried by either node.
- Add disabled-by-default production Compose and Helm examples, secret wiring,
  configuration validation, and Chinese/English receiver guidance.
- Add release acceptance coverage for HTTP publication without weakening the
  existing NSQ checks.

Acceptance criteria:

- Operators can tell whether the publisher is disabled, pending, failed, or
  published without seeing message bodies or secrets.
- A two-node deployment cannot intentionally publish one claimed event in
  parallel from both nodes.
- The release image, Compose references, Helm chart, CI, and E2E tests cover
  the supported configuration.

## Suggested Implementation Order

1. Add the V13 configuration structs, validation, and contract documentation.
2. Implement the HTTP adapter with focused signing and response tests.
3. Wire it through the terminal publisher factory and existing outbox worker.
4. Add single-node then cluster E2E with a controlled webhook receiver.
5. Add operational visibility, deployment examples, and release guidance.

## Completion Criteria

`v0.13.0` is complete when:

- a terminal event can be published reliably to a signed HTTP endpoint;
- receiver-visible event identity is stable and documentable for de-duplication;
- HTTP failure uses the same durable retry and cluster claim behavior as NSQ;
- no arbitrary business body or secret leaks through the event, logs, metrics,
  diagnostics, or console;
- all existing terminal publisher modes remain backward-compatible; and
- configuration, tests, Compose, Helm, CI, and English/Chinese operations
  documentation cover the feature.

## Known Boundaries

- HTTP receiver business processing remains outside the gateway transaction.
- Receivers must persist `event_id` before performing non-idempotent business
  work.
- The gateway does not validate the receiver's business authorization result;
  it only observes the HTTP transport response.
- TLS, mTLS, network policy, and receiver availability remain deployment
  responsibilities.
