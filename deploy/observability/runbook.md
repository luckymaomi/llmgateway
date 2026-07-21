# LLMGateway 运行告警

Prometheus 从 backend 网络分别抓取 `gateway-a:8080/metrics` 与 `gateway-b:8080/metrics`。Caddy 对公网 `/metrics` 返回 404。所有查询只使用 route、status、outcome、operation、resource domain、Provider kind 和 canonical error kind；禁止添加用户、Key、模型输入、credential ID 或上游 URL 标签。

## Not Ready

先分别请求两个实例的 `/health/ready`，再检查 PostgreSQL `pg_isready`、Valkey `PING`、磁盘空间和连接池。一个实例失败时让 Caddy 摘除它；两个实例均失败时停止新流量。恢复存储后确认 readiness、quota reservation 和 request recovery 不再失败，再逐实例恢复。不要用跳过 readiness 或 fail-open 绕过存储故障。

## Admission Saturated

比较 `queue_full`、`timeout`、`capacity_exhausted`，并查看 active leases、HTTP p95/p99、PostgreSQL acquire wait、Valkey latency 和主机 RSS/CPU。单次 300 人突发的受控 429 不升级事故；持续 10 分钟才行动。先限制入口/降载，再按 `architecture.md` 的容量信号增加 Gateway 或调整有真实 Provider 额度依据的域级容量，不能放宽成员/Key/credential 硬限制。

## Provider Failures

按 `provider_kind` 和 `error_kind` 区分 authentication/quota/rate_limit/temporary/uncertain。查看 credential probe 和 active revision，但不输出 secret。401/403 停用并人工修复对应 credential；429 遵守冷却与 `Retry-After`；5xx 仅在 replay-safe 边界有界切换。免费池耗尽不得转入付费池。

## Uncertain Outcomes

立即记录 request ID、attempt 状态、credential prefix 和时间窗口，禁止客户端或值班人员自动重放。确认 PostgreSQL request/attempt/reservation 为 `uncertain+reserved`，对照 Provider 账单/请求 ID人工裁决；无法权威裁决时继续保留预留。流已提交时只向客户端发送终止事件，不拼接另一 Provider。

## Quota Failures

停止新增请求并检查 PostgreSQL 事务、锁、磁盘和 migration。对照 requests、ledger reservations/events 与 usage；任何重复 settle/release 必须保持幂等。恢复后先运行定向账本不变量，再开放流量，不直接修改余额凑平。

## Background Failures

检查 response record、execution generation/heartbeat、关联 request 终态和 worker ERROR。多实例 ownership loss 是正常幂等竞争；持续 claim/storage/fenced 错误才是故障。进程强杀后等待 stale/recovery 窗口，确认每条记录只收敛一次且未知上游副作用不重放。

## Account Recovery

成员忘记密码时，管理员在“用户”页执行“重置密码”。操作使用 CSRF 与 idempotency key，同一事务更新 Argon2id hash、撤销成员全部活动会话并写 `identity.member_password_reset` audit；通过撤销数确认旧客户端已下线。不要经聊天、工单或日志传递临时密码。

管理员只需清理其他登录位置时，在自己的用户行执行“撤销会话”；当前操作会话被保留，其他活动会话在同一事务撤销。唯一管理员无法登录时，从受控运维主机执行 `dbtool -action recover-administrator`，必须同时提供管理员邮箱、只读 password file 和 `-confirm-account-recovery`。命令只允许恢复 administrator，激活账号、撤销全部旧会话并写 system actor audit；完成后立即删除 password file，再从新的浏览器会话登录并检查 audit。匿名公网找回不存在。

## Gateway Key Replacement

在活动 Key 行执行“更换”，保存只展示一次的新 Key。新 Key 原子继承同一 owner、模型范围和到期时间，旧 Key 保持 active；先让客户端改用新 Key，并用 `/v1/models` 与最小业务请求确认。确认旧 Key 最近使用时间不再变化后，单独执行“撤销”并再次验证新 Key。replacement 响应丢失时只能用原 idempotency key 重试，不能创建另一条替换操作；完整 secret 不进入浏览器存储、审计、指标或日志。

## Backup And Disaster Recovery

每班检查 `llmgateway-backup.timer`、最近一次 service 退出码、最后快照时间和 Restic check。最后成功快照超过 6 小时、远端 backend 不可达或 check 失败立即告警；不要删除锁、跳过 check 或把本机 staging 当备份。数据库、主密钥与 pepper 同属恢复集合，Restic password 和远端凭据必须在另一保管路径。

主机或卷丢失时停止旧入口写流量，在新主机只向空目录恢复最新已验证快照，只向新数据库执行 `pg_restore`。依次核对 dump SHA-256、migration version、管理员/成员登录、成员管理 API 403、active revision、Provider credential 可解密、Key/额度/账本，再启动 Caddy。DNS/TLS 切换后观察 readiness、5xx、quota/recovery 指标和日志至少一个业务窗口。回切使用恢复库和逐实例 readiness；未知副作用请求保持 uncertain，不因灾备自动重放。
