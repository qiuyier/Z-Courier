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

批量推送使用：

```text
POST /internal/push/batch
```

如果部分消息失败，HTTP 可能返回 `207 Multi-Status`，响应里会给出每条消息的结果。

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
