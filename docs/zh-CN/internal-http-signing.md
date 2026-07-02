# 内部 HTTP HMAC 签名

Z-Courier 的内部 HTTP API 可以使用 HMAC-SHA256 签名认证。它用于：

- 后端调用 gateway 的 `/internal/*` API。
- gateway 节点之间的 peer push。

HMAC 能证明请求没有被篡改，并通过 timestamp + nonce 抵抗重放。它不负责加密，
生产环境仍然应该配合 TLS、mTLS、内网或 service mesh。

## 请求头

每个签名请求都带这些头：

```text
X-ZCourier-Key-ID: backend-2026-01
X-ZCourier-Timestamp: 1780000000
X-ZCourier-Nonce: <unpadded Base64URL, 16-64 random bytes>
X-ZCourier-Signature: <unpadded Base64URL HMAC-SHA256>
```

规则：

- timestamp 是 Unix 秒。
- key id 长度 1-128，可见 ASCII。
- HMAC secret 至少 32 字节。
- nonce 应该使用安全随机数。

## Canonical String

签名输入是 7 行 UTF-8 文本，中间用 `\n` 分隔，末尾没有额外换行：

```text
ZCOURIER-HMAC-SHA256
<timestamp header>
<nonce header>
<uppercase HTTP method>
<escaped path or />
<canonical query>
<lowercase hexadecimal SHA-256 body digest>
```

query 规范化规则：

1. 按 UTF-8 form data 解析 query，`+` 表示空格。
2. 除 `A-Z a-z 0-9 - . _ ~` 外，每个字节都 percent-encode。
3. 空格编码为 `%20`，十六进制使用大写。
4. 先按编码后的 key 排序，再按 value 排序。
5. 用 `key=value` 和 `&` 拼接。没有 query 时这一行为空。

示例：

```text
POST /internal/push?b=two&a=1&a=0
timestamp: 1780000000
nonce: MDEyMzQ1Njc4OWFiY2RlZg
body: hello
```

canonical string：

```text
ZCOURIER-HMAC-SHA256
1780000000
MDEyMzQ1Njc4OWFiY2RlZg
POST
/internal/push
a=0&a=1&b=two
2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
```

然后用选中的 secret 对这些字节做 HMAC-SHA256，结果用无 padding 的 Base64URL 编码。

## 重放保护

网关只接受 `max_clock_skew` 窗口内的 timestamp。签名验证通过后，会把
`key_id + nonce` 存入本地 nonce store，直到 nonce 过期。

同一个 nonce 再次出现会返回 `401`。nonce store 有容量上限，如果清理过期 nonce 后
仍然满了，会 fail closed，返回 `503 auth_unavailable`。

当前 nonce store 是单 gateway 进程本地的。如果一个后端地址会负载均衡到多个 gateway
节点，生产环境建议使用会话亲和、固定节点地址，或等待后续共享 nonce store 能力。

## 密钥轮换

推荐无停机轮换：

1. 在 gateway 配置里增加新 `key_id` 和新 secret，部署。
2. 后端 SDK 或 peer 配置切换到新 `key_id`。
3. 等待超过最大请求生命周期和部署重叠时间。
4. 从 gateway 删除旧 key。

不要把 HMAC secret 写进 Git。使用环境变量、Secret Manager 或 Kubernetes Secret 注入。

## 保护哪些路径

backend HMAC 保护后端调用的 `/internal/*` API，例如：

- `/internal/push`
- `/internal/push/batch`
- `/internal/messages`
- `/internal/message/requeue`
- `/internal/message/discard`
- `/internal/admin/*`
- `/internal/debug/*`

gateway peer HMAC 独立保护：

```text
POST /internal/cluster/push
```

backend 和 peer 使用不同 key ring 和 nonce store。

这些探针路径不签名：

- `/healthz`
- `/readyz`
- `/metrics`

## 监控

相关指标：

```text
z_courier_internal_http_signature_total{result=...}
z_courier_cluster_peer_signature_total{result=...}
```

`result` 是有限集合，例如 `success`、`replay`、`expired`、`invalid_signature`。
secret、key id、调用方身份不会作为 metric label。
