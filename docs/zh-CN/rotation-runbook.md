# 密钥与证书轮换操作手册

本手册用于无协议变更地轮换 Z-Courier 的 HMAC 密钥与边缘 TLS/mTLS 证书。轮换不会
修改数据包格式、PostgreSQL schema、Redis 状态或 NSQ topic。一次只处理一个安全边界，
不要把密钥轮换与无关的 gateway 升级混在同一次发布中。

## 开始前

1. 记录当前部署 revision、active key ID、证书序列号、Secret 版本和可回滚 revision。
2. 新 HMAC secret 至少使用 32 个随机字节；证书由生产 PKI 或证书管理平台签发。
   `scripts/generate_edge_test_certs.sh` 仅能用于一次性的本地测试。
3. 确认所有目标 gateway 已 ready，控制面指标稳定。旧 secret 与旧证书材料只保留在
   已批准的 secret manager 中，直到观察窗口结束。
4. 在预发执行对应验收：

   ```bash
   bash scripts/e2e_cluster.sh
   bash scripts/certificate_rotation_smoke.sh
   bash scripts/helm_hmac_rotation_check.sh
   ```

5. 在生产变更前准备明确的回滚变更。回滚只恢复原 Secret 引用或证书文件，不回滚
   数据库，也不会重发已经被接收的数据包。

绝不能把 HMAC secret、私钥、CA 私钥写入 Helm values、ConfigMap、浏览器代码、日志、
诊断信息、工单或 shell history。

## HMAC 密钥轮换

Z-Courier 在三个独立边界使用同一套带时间戳的签名协议：

| 边界 | gateway 验签配置 | 当前出站签名方 |
| --- | --- | --- |
| backend 到 gateway | `internal_http.auth.hmac.keys` | backend SDK 或 backend signer |
| gateway peer push | `cluster.peer.auth.hmac.keys` | `cluster.peer.auth.hmac.key_id` |
| 终态 HTTP webhook | receiver 自己维护的 keyring | `downlink.terminal.publisher.http.hmac.key_id` |

`keys` 是允许验签的 keyring。peer 的 `key_id` 只选择一把出站签名密钥。终态
webhook receiver 位于 gateway 外部，因此滚动发布期间它必须自行同时接受旧、新 key ID。

### 1. 先扩大验签集合

任何 signer 切换前，先在所有 verifier 中增加新 key ID 和 secret。

- backend-to-gateway 与 peer HMAC：所有 gateway pod 的 keyring 都同时放入旧、新 key。
- terminal webhook：先让 receiver 接受旧、新两把 key。
- Helm：新 active secret 保持在主 `secretEnv`，旧验签 key 放入 `additionalKeys`，两者
  都从已有 Kubernetes Secret 注入。参考
  [values-hmac-rotation.yaml](../../deploy/helm/z-courier/examples/values-hmac-rotation.yaml)。

此阶段不能删除旧 key。通过 readiness 和 deployment 状态确认每个 pod 已加载目标配置，
但不要打印 secret 值。

### 2. 再滚动 signer

确认所有 verifier 都接受新 key ID 后才切 signer：

1. 将 backend SDK 或 backend signer 切到新的 internal HTTP key。
2. peer HMAC：把 `cluster.peer.auth.hmac.key_id` 改成新 ID，但在 `keys` 中继续保留
   旧、新两项，然后一次只滚动一个 gateway pod。
3. terminal webhook：逐个 gateway pod 更新新的 terminal `key_id` 与 secret。旧 pod
   drain 完成、终态重试 lease 过期前，receiver 继续保持双 key。

每个 pod 都要等到 ready 后再继续，不能同时 drain 所有集群节点。已有 TCP 连接可以在
drain 时重连，但新的 AUTH/BIND 必须持续成功。

### 3. 观察重叠窗口

| 信号 | 正常表现 | 需要排查的情况 |
| --- | --- | --- |
| `z_courier_internal_http_signature_total` | `success` 持续出现 | `invalid_signature`、`expired`、`replay` 或 `auth_unavailable` 上升 |
| `z_courier_cluster_peer_signature_total` | `success` 持续出现 | peer push 被签名校验拒绝 |
| terminal webhook receiver | 能接收旧、新 key ID 的事件 | 401/403、重复投递，或终态事件缺失 |
| `z_courier_downlink_terminal_publish_total` 与重试指标 | 发布持续推进 | 终态事件一直 pending 或发布失败上升 |
| `/readyz`、route lookup、peer push | 每个节点健康 | 已滚动节点不 ready 或路由仍指向已 drain 节点 |

receiver 可以在自己的 audit 中记录 key ID，但 Z-Courier 不把 key ID 作为 Prometheus
label。诊断信息中绝不能包含 secret 字节。

### 4. 退役旧 key

至少等待以下时间全部过去：最大请求生命周期、`max_clock_skew`、nonce TTL、负载均衡
drain 时间、terminal retry lease，以及旧 gateway pod 与旧 backend signer 都退出。

之后从 verifier 删除旧 key，并删除它的外部 Secret 引用。Helm 要在后续部署中同时移除
对应的 `additionalKeys` 和 `extraEnv`。secret manager 中的恢复历史按组织保留策略处理。

### HMAC 回滚

签名切换后出现失败时：

1. 恢复上一把 active signer 的 key ID 与 secret 引用。
2. 回滚滚动期间继续让两把 key 都能验签。
3. 验证内部签名请求、peer push 与 terminal 发布。
4. 在新的稳定观察窗口完成前，不删除任一 key。

恢复旧 active key 与协议兼容，不需要数据迁移。不要为了“清理”签名问题而删除排队中的
downlink 消息。

## TLS 与 mTLS 证书轮换

TLS 在审查过的 Nginx、Caddy、负载均衡或平台 ingress 边缘终止，边缘之后的 gateway
包格式不变。对于 mTLS 机器 listener，服务端证书信任与客户端证书信任是两套独立集合，
都必须分别重叠。

### 1. 先扩大信任集合

替换服务端证书前：

1. 将新签发 CA 加入每个客户端 trust bundle，同时保留旧 CA。
2. 如果 mTLS 客户端证书也轮换，把新 client CA 加入边缘 `client-ca.crt` trust bundle，
   同时保留旧 client CA。
3. 代理只挂载 `tls.crt`、`tls.key` 与公开的 `client-ca.crt` bundle，CA 私钥不能进入
   代理 container 或 pod。
4. 双信任 bundle 就绪后，再 reload 或滚动 edge。

Kubernetes 中更新外部 Secret 或证书管理器引用，再做受控滚动 reload。不能把 PEM 内容
写到 chart values 或 ConfigMap。

### 2. 逐步替换证书

一次只替换一个 edge 实例的服务端证书和私钥：

- Nginx 可在原子更新挂载证书文件后执行受控 `nginx -s reload`。
- 负载均衡和 ingress 使用其平台提供的滚动证书更新能力。
- 每次更新都至少保留一个健康 edge 实例，并保留长连接的 TCP drain 与 idle timeout。

每台实例更新后，用双 trust bundle 建立新的 TLS handshake。mTLS 重叠期间必须分别验证
旧、新客户端证书。已有连接可以结束或重连；Go SDK 重连会建立新的 TLS handshake，修改
本地 CA 文件后需要重新创建 Client。

### 3. 退役旧信任

旧服务端证书、旧客户端证书及活跃连接都 drain 后：

1. 从客户端 trust bundle 删除旧 CA。
2. 从 mTLS listener 的 trust bundle 删除旧 client CA。
3. 再次逐步 reload 或滚动 edge。
4. 验证新证书客户端成功、旧证书客户端被拒绝。

### 证书回滚

新证书的校验、SNI 或 mTLS 验证失败时：

1. 恢复上一个服务端证书和私钥引用。
2. 若属于 mTLS 问题，同时恢复之前的 client CA bundle。
3. 每次只 reload 或滚动一个 edge，并验证新的旧信任 handshake。
4. 事故处理完成并批准新的轮换方案前，继续保留双信任 bundle。

常见失败信号包括：代理 TLS handshake error、客户端 `unknown authority`、因客户端
证书校验导致的 Nginx `400`、edge 5xx 上升，以及新的 AUTH/BIND session 减少。

## 证据与完成标准

变更记录中只附以下不敏感证据：

- Secret 或证书管理器版本标识、active key ID。
- deployment revision、pod readiness、edge reload 或 rollout 时间。
- 变更前、中、后的签名结果、peer push、terminal 发布、连接健康指标快照。
- 相关 smoke 的输出或 CI 链接。
- 旧 key ID 与旧证书信任的准确退役时间。

只有当观察窗口结束、所有 active signer 与证书都使用新材料、旧信任已删除、正常流量
稳定，且仍能从已批准的 secret 或证书管理器恢复回滚材料时，轮换才算完成。
