# Go SDK 使用

Go SDK 位于：

```text
pkg/sdk
```

它分成几个公共包：

| 包 | 用途 |
| --- | --- |
| `pkg/sdk/protocol` | Z-Courier 内层 packet 编解码 |
| `pkg/sdk/client` | 高层 TCP 客户端，处理连接、AUTH/BIND、ACK、重连、下行 |
| `pkg/sdk/backend` | 后端调用 gateway internal HTTP 的客户端 |
| `pkg/sdk/signing` | HMAC 签名公共逻辑 |

## protocol 包

导入：

```go
import "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
```

创建并编码 packet：

```go
packet := protocol.NewPacket(1001, []byte(`{"action":"ping"}`))
packet.ClientID = "client-1"
packet.DeviceID = "device-1"
packet.Token = "client-token"
packet.MessageID = "message-1"
packet.TraceID = "trace-1"
packet.Flags = protocol.FlagAckRequired

data, err := protocol.Encode(packet)
if err != nil {
    return err
}
```

解码：

```go
packet, err := protocol.Decode(data)
if err != nil {
    return err
}

if packet.MsgID == protocol.MsgIDAck {
    ack, err := protocol.DecodeAck(packet)
    if err != nil {
        return err
    }
    _ = ack
}
```

保留 MsgID：

```go
protocol.MsgIDAck         // 1
protocol.MsgIDDownlinkAck // 2
protocol.MsgIDBind        // 1000
```

业务配置校验时可以用：

```go
if protocol.IsReservedMsgID(msgID) {
    return fmt.Errorf("reserved msg id")
}
```

## client 包

`client` 包适合写真正的长连接客户端。

典型职责：

- 建立 TCP 连接。
- 发送 AUTH/BIND。
- 等待 gateway ACK。
- 发送上行业务消息。
- 接收下行消息。
- 自动或手动发送下行 ACK。
- 连接断开后按配置重连。

示意代码：

```go
cfg := client.Config{
    Addr:     "127.0.0.1:8999",
    ClientID: "client-1",
    DeviceID: "device-1",
    TokenProvider: client.StaticToken("client-token"),
}

c, err := client.Dial(ctx, cfg)
if err != nil {
    return err
}
defer c.Close()

ack, err := c.Send(ctx, client.SendRequest{
    MsgID:     2001,
    MessageID: "message-1",
    TraceID:   "trace-1",
    Body:      []byte("hello"),
    WaitAck:   true,
})
if err != nil {
    return err
}
_ = ack
```

具体 API 以 `pkg/sdk/client` 代码为准。项目里的 `cmd/devclient` 已经改为基于 SDK，
可以作为调试和学习入口。

## 下行处理

如果业务希望自动 ACK，可以注册 handler，让 SDK 在处理后自动回 ACK。

如果业务处理需要严格控制，例如写数据库成功后再 ACK，就使用手动 ACK：

```text
收到下行 -> 业务落库/去重 -> 成功后发送 MsgID=2 ACK
```

注意：进程内 LRU 去重只是客户端保护，进程重启后记录会消失。重要业务仍要在业务
数据库里对 `MessageID` 做持久化去重。

## backend 包

后端应用不需要建立 TCP 连接。它只需要调用 gateway internal HTTP。

导入：

```go
import "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
```

token 模式：

```go
client, err := backend.NewClient(backend.Config{
    BaseURL:       "http://gateway:18080",
    InternalToken: os.Getenv("ZCOURIER_INTERNAL_TOKEN"),
    Timeout:       3 * time.Second,
})
if err != nil {
    return err
}
```

HMAC 模式：

```go
client, err := backend.NewClient(backend.Config{
    BaseURL: "https://gateway.internal:18080",
    HMAC: &backend.HMACConfig{
        KeyID:  "backend-2026-01",
        Secret: []byte(os.Getenv("ZCOURIER_INTERNAL_HMAC_SECRET")),
    },
    Timeout: 3 * time.Second,
})
```

推送下行消息：

```go
response, err := client.Push(ctx, backend.PushRequest{
    ClientID:    "client-1",
    DeviceID:    "device-1",
    MsgID:       2001,
    MessageID:   "order-event-42",
    TraceID:     "trace-42",
    AckRequired: true,
    Body:        payload,
})
if err != nil {
    return err
}

switch response.DeliveryState {
case backend.DeliveryStateSent:
    // 已写入在线客户端连接
case backend.DeliveryStateQueued:
    // 客户端离线或需要可靠投递，已进入存储
}
```

启用可靠存储后，可以通过 `SubmissionState` 区分首次提交和幂等重放：

```go
switch response.SubmissionState {
case backend.SubmissionStateCreated:
    // 本次请求创建了持久消息
case backend.SubmissionStateExisting:
    // 相同 MessageID 已存在，MessageStatus 是当前持久状态
}
```

如果相同 `MessageID` 对应的 client、device、MsgID、ACK 要求或 Body 不同，
gateway 会返回 HTTP `409` 和 `message_id_conflict`；`Client.Push` 会将它返回为
`*backend.APIError`，且不会覆盖原消息。

常用方法：

```go
client.Push(ctx, request)
client.PushBatch(ctx, batchRequest)
client.GetMessage(ctx, "message-id")
client.ListMessages(ctx, backend.ListMessagesRequest{Status: backend.MessageStatusFailed})
client.Requeue(ctx, "message-id")
client.Discard(ctx, "message-id", "operator decision")
```

非 2xx 响应会返回 `*backend.APIError`。不要解析错误字符串，使用 `errors.As`：

```go
var apiErr *backend.APIError
if errors.As(err, &apiErr) && apiErr.Retryable() {
    // 做有界退避重试，复用同一个 MessageID
}
```

## 什么时候用哪个包

- 自己写客户端 SDK：用 `protocol`，必要时实现 Zinx 外层帧。
- Go 客户端业务程序：用 `client`。
- 后端服务调用下行推送：用 `backend`。
- 自己实现 HMAC 跨语言签名：参考 `signing` 和 [内部 HTTP HMAC 签名](internal-http-signing.md)。
