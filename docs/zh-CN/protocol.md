# 协议说明

Z-Courier 客户端协议由两层组成：

1. 外层是当前 Zinx TCP listener 使用的消息帧。
2. 内层是 Z-Courier 自己的二进制 packet。

业务方一般不用手写这两层，优先使用 Go/PHP SDK。自己实现其他语言 SDK 时，需要按
这里的规则编码。

## 内层 Packet

当前公共 SDK 的 canonical 实现是：

```text
pkg/sdk/protocol
```

所有整数都是 big-endian。

固定头大小是 41 字节：

| 字段 | 类型/大小 | 说明 |
| --- | ---: | --- |
| Magic | uint16 / 2 | 固定 `0x5A43`，用于识别 Z-Courier packet |
| Version | uint8 / 1 | 当前为 `1` |
| Flags | uint16 / 2 | bit flags，例如 ACK required、compressed |
| MsgID | uint32 / 4 | 控制命令或业务路由 ID |
| Seq | uint64 / 8 | 连接内序号，由客户端生成 |
| Timestamp | int64 / 8 | Unix 毫秒时间戳 |
| ClientIDLen | uint16 / 2 | `ClientID` 字节长度 |
| DeviceIDLen | uint16 / 2 | `DeviceID` 字节长度 |
| SessionIDLen | uint16 / 2 | `SessionID` 字节长度 |
| MessageIDLen | uint16 / 2 | `MessageID` 字节长度 |
| TraceIDLen | uint16 / 2 | `TraceID` 字节长度 |
| TokenLen | uint16 / 2 | `Token` 字节长度 |
| BodyLength | uint32 / 4 | body 字节长度 |

可变区按这个顺序拼接：

```text
ClientID
DeviceID
SessionID
MessageID
TraceID
Token
Body
```

字符串字段最大 65535 字节。body 使用 `uint32` 表示长度，但实际部署应该配置更小的
业务上限。SDK 默认 decode body 上限是 4 MiB。

## 重要字段

- `ClientID`：客户端声明的身份。鉴权前不可信。
- `DeviceID`：设备身份。用于连接绑定和定向推送。
- `SessionID`：网关签发的会话 ID。客户端 bind 前可以为空。
- `MsgID`：路由和控制命令的核心字段。
- `MessageID`：消息唯一 ID，用于 ACK、查询、重试、业务幂等。
- `TraceID`：链路追踪 ID。
- `Token`：客户端鉴权 token。
- `Body`：业务消息体，网关不解析。

## 保留 MsgID

| MsgID | 方向 | 含义 |
| ---: | --- | --- |
| `1` | gateway -> client | 网关 ACK |
| `2` | client -> gateway | 下行 delivery ACK |
| `1000` | client -> gateway | AUTH/BIND |

业务 `MsgID` 不能占用这些值。示例配置里通常使用：

```text
1001-1999  HTTP upstream 示例
2000-2999  NSQ upstream 示例
```

这些只是示例范围，生产环境应该按业务模块自己划分。

## AUTH/BIND

客户端连接后第一件事应该是发送：

```text
MsgID = 1000
```

这个包必须包含：

```text
Token
DeviceID
```

它可以带 `ClientID`，但最终绑定身份以 token verifier 返回的 `client_id` 为准。

绑定成功后，网关记录：

```text
client_id + device_id -> session/connection
```

如果启用了可靠下行存储，bind 成功还会触发 pending 消息补发。

`AUTH/BIND` 是网关控制消息，不会转发给后端 upstream。

## 网关 ACK

网关 ACK 使用：

```text
MsgID = 1
```

ACK body 是 JSON：

```json
{
  "message_id": "message-1",
  "msg_id": 1000,
  "code": "accepted",
  "reason": "",
  "trace_id": "trace-1"
}
```

常见 `code`：

- `accepted`：网关已接受。
- `rejected`：网关拒绝。
- `unauthorized`：token 验证失败。
- `auth_unavailable`：鉴权服务临时不可用，客户端可以退避重试。
- `decode_failed`：二进制包解析失败。

常见 `reason`：

- `rate_limited`：入口限流。
- `overloaded`：upstream 或内部容量限制拒绝。
- `route_not_found`：没有匹配的 upstream route。

## 下行推送

后端通过内部 HTTP 推送：

```text
POST /internal/push
```

请求示例：

```json
{
  "client_id": "dev-client",
  "device_id": "device-1",
  "msg_id": 2001,
  "message_id": "message-1",
  "trace_id": "trace-1",
  "ack_required": true,
  "body": "aGVsbG8="
}
```

JSON 里的 `body` 是 base64，因为 Go JSON 对 `[]byte` 会这样编码。Z-Courier
仍然把它当 opaque bytes。

启用可靠存储后，`message_id` 同时也是下行提交的幂等键。gateway 会比较
`client_id`、`device_id`、`msg_id`、`ack_required` 和不透明 Body 摘要；
`trace_id` 不属于不可变身份。

- 新消息返回 `submission_state = created`，然后正常发送或排队。
- 相同身份的重复提交返回 HTTP `200`、`submission_state = existing`，并通过
  `message_status` 返回已有状态，不会再次触发首次投递。
- 同一 `message_id` 使用了不同不可变身份时返回 HTTP `409`，响应码为
  `message_id_conflict`，原消息不会被覆盖。

这个契约保护的是 backend 到 gateway 的提交重试，不代表客户端或业务处理是
exactly-once。未启用可靠存储的部署不会保存幂等记录。

批量推送使用：

```text
POST /internal/push/batch
```

如果部分消息失败，HTTP 可能返回 `207 Multi-Status`，响应里会给出每条消息的结果。

可靠队列容量已满时，新消息返回 HTTP `429`：

```json
{
  "code": "queue_capacity_exceeded",
  "capacity_scope": "device",
  "capacity_limit": 1000,
  "capacity_pending": 1000,
  "client_id": "dev-client",
  "device_id": "device-1",
  "message_id": "message-1"
}
```

`capacity_scope` 是 `global` 或 `device`。后端之后可以退避重试，但必须复用同一个
`message_id`。这次请求没有被网关接受或持久化；兼容的幂等重放会先于容量检查，
仍然返回已有状态。

## 查询下行状态

后端可以通过 `message_id` 查询可靠下行消息：

```text
GET /internal/message/status?message_id=message-1
```

响应包含当前投递状态、尝试次数、重试时间和命中的策略，例如：

```json
{
  "code": "ok",
  "message_id": "message-1",
  "client_id": "dev-client",
  "device_id": "device-1",
  "msg_id": 2001,
  "policy_name": "critical",
  "status": "delivered",
  "attempts": 1,
  "body_size_bytes": 5
}
```

对于 V12.2.2 之后接收的新消息，`policy_name` 是消息首次被可靠存储接受时选中的
策略快照名称。即使之后修改网关配置，存量消息仍按已保存的策略参数执行，因此这个
字段可以帮助排查它为什么采用当前的 ACK、重试和终止限制。升级前没有快照的旧行会
按当前 MsgID 策略回退解析。

`failed` 和 `discarded` 消息还可能返回 `terminal_reason`、`terminal_at`、
`terminal_publish_status`、`terminal_publish_attempts`、
`terminal_next_publish_at`、`terminal_publish_error` 与
`terminal_published_at`。发布状态为 `disabled`、`pending`、`failed` 或
`published`。

## 终态事件协议

当 `downlink.terminal.publisher.type` 配置为 `nsq` 时，网关向指定 topic 发布
如下版本化 JSON：

```json
{
  "version": 1,
  "type": "z_courier.downlink.terminal",
  "event_id": "message-1:failed",
  "message_id": "message-1",
  "client_id": "dev-client",
  "device_id": "device-1",
  "msg_id": 2001,
  "trace_id": "trace-1",
  "terminal_status": "failed",
  "terminal_reason": "max_attempts_exceeded",
  "policy_name": "critical",
  "attempts": 10,
  "message_created_at": "2026-07-12T08:00:00Z",
  "terminal_at": "2026-07-12T08:01:00Z",
  "gateway_node": "gateway-a"
}
```

这个事件故意不包含 `body`，也不会输出内部 token、HMAC 材料或存储凭据。
发布语义是 at-least-once，消费者必须使用 `message_id + terminal_status`
（等价于确定性的 `event_id`）去重。一条消息可以先产生 `failed` 事件，之后被
管理员 discard 时再产生一条独立的 `discarded` 事件。

终态事件使用独立的持久化 outbox 和重试状态，发布失败不会重新触发客户端投递，
也不会反复改变消息终态。状态接口会显示发布失败及后续成功的结果。

## 下行 ACK

如果下行消息要求 ACK，客户端收到后应该发送：

```text
MsgID = 2
```

ACK 里要带原始下行消息的 `MessageID`。网关收到后会把可靠存储里的状态更新为
`delivered`。如果 ACK 超时，消息会进入 retry。

## MessageID 和幂等

`MessageID` 是可靠系统的关键字段。

网关会用它关联 ACK、查询状态、重试、requeue、discard。但 Z-Courier 不是业务数据库，
不能替你完成业务 exactly-once。

建议：

- 后端生成全局唯一 `MessageID`。
- 业务数据库对关键操作建立唯一约束。
- 客户端和后端都把重复消息当作正常情况处理。
