# LLMGateway Architecture

本文记录当前技术栈、事实 owner、运行拓扑和管理端结构。产品合同以 [`spec.md`](spec.md) 为准，当前实施状态以 [`plan.md`](plan.md) 为准。

## 技术基线

| 层 | 选择 | 职责 |
| --- | --- | --- |
| 服务端 | Go 1.26 模块化单体 | 公共 API、管理 API、后台恢复和生产前端交付 |
| HTTP | `net/http` + `chi` | 请求上限、取消、流式生命周期和中间件 |
| 持久化 | PostgreSQL 18 | 身份、catalog revision、请求、attempt、额度、usage、mutation 和审计的唯一持久 owner |
| SQL | `pgx` + `sqlc` + 基线 migration | 显式事务与确定性生成类型 |
| 协调 | Valkey 9 | RPM/TPM、并发租约与跨实例短期容量；不拥有账本、配置或排队顺序 |
| 管理端 | React 19 + TypeScript + Vite 8 | 生产构建嵌入 Go 二进制 |
| 前端状态 | TanStack Router/Query/Table | 路由、服务端 cache 和数据表格 |
| UI | Radix 原语、Lucide、CSS tokens | 可访问、信息密集、桌面与移动可操作 |
| 运行证据 | 结构化日志 + Prometheus 指标 | request ID、稳定错误类别、容量与恢复结果，默认脱敏 |

模块化单体适合约 200～300 名受控用户：部署和恢复边界清楚，同时各领域仍按事实 owner 隔离。只有出现独立扩容或发布需求时才拆服务。

## 运行拓扑

```text
Administrator / Member Browser
              |
              v
       Control API + embedded Web
              |
Client SDK -> Public /v1 API -> Request Workflow -> Provider Adapter -> Upstream
              |                     |
              v                     v
         PostgreSQL <----------> Valkey
       persistent facts       expiring coordination
```

- 单个 Go 进程同时提供管理 API、公共 API、恢复 worker 和嵌入式生产前端。
- PostgreSQL 故障时不能接受需要持久事实的新工作；Valkey 故障时不能绕过共享容量。
- 多实例共享 PostgreSQL 与 Valkey。请求执行通过持久 claim/heartbeat 和过期租约协调，强杀后由存活实例或重启实例恢复。

## 模块边界

```text
internal/
  publicapi/       /v1 鉴权、协议响应和流式边界
  protocol/        OpenAI-compatible wire 解析与呈现
  canonical/       消息、工具、reasoning、usage 和错误语义
  providers/       Provider adapter、能力与错误分类
  identity/        用户、角色、会话、邀请、Gateway Key 与模型授权
  registry/        Provider、模型、凭据、绑定、健康与冷却
  configuration/   catalog revision、发布、回滚与 active version
  admission/       本地每用户 FIFO、用户轮转和并发上限
  coordination/    Valkey 原子限流与租约
  routing/         硬资格、管理员优先级和同级权重选择
  quota/           额度预留、结算、补偿与 usage
  requestflow/     接受、执行、发送、流式、结算与恢复编排
  execution/       持久执行 claim 与 fencing
  responses/       后台 Responses 状态机
  security/        AEAD、摘要、SSRF 与网络策略
  controlapi/      管理 HTTP 合同与权限边界
  store/           PostgreSQL/Valkey adapter 与 sqlc 消费者
  app/             配置校验、依赖装配和进程生命周期
```

Public API、Canonical Model 和 Provider Adapter 分别拥有外部合同、内部语义和厂商差异。调度不解析厂商 wire，adapter 不判断用户额度，handler 不重算领域状态。

`providers.Catalog` 是 Provider kind、展示名称和 adapter builder 的唯一注册事实。请求执行、凭据探测、Provider 写入校验和管理端类型列表都从 catalog 读取；Catalog 在构建时固定并随受审查版本发布。

模型的 `capabilities` JSON 同时保存 reasoning 控制 profile。Registry 校验 reasoning 与 profile 必须一致，不可变 revision 原样捕获；`requestflow.ProviderFactory` 只把 `toggle`、`effort`、`hybrid` 投影为 adapter capability。通用 adapter 据此选择 `enable_thinking` 或 `reasoning_effort`，不按 Provider 名称分支。

## 请求数据流

```text
校验 Gateway Key 与模型授权
  -> 本地公平 admission + Valkey 共享容量
  -> PostgreSQL 原子额度预留并持久接受请求
  -> 已发布 catalog 解析模型与候选凭据
  -> 硬资格过滤 -> 最小 priority -> 同级 weighted choice
  -> 取得 Provider/credential/用户/Key 多维租约
  -> 解密最小必要 secret，SSRF 安全客户端发送
  -> 非流/流式响应归一
  -> 权威 usage 结算，或按发送事实释放/保持 uncertain
```

冷却、有限重试和熔断只消费稳定错误类别。发送事实未知、流已提交或请求不可安全重放时不切换 Provider。

## 配置发布

1. 管理员编辑 live registry。
2. 捕获操作在 PostgreSQL 事务内复制 Provider、模型、凭据非秘密字段和路由绑定，生成不可变 revision 与 checksum。
3. 发布以 expected active version 做并发控制，原子切换唯一 active revision。
4. 数据面只读取 active revision；Valkey 不保存第二套配置快照。
5. 回滚是把一个已有 revision 重新设为 active，并递增 active version。

## 身份与管理端

- `administrator`：Provider、模型、凭据、发布、用户、额度和任意用户 Key。
- `member`：自己的 Key、授权、额度、用量和 Playground。
- 导航按 capability 生成，不靠隐藏按钮替代服务端授权。
- 一次性 secret 只在创建/幂等恢复响应中出现，不进入 Query cache、localStorage、sessionStorage、截图或 trace。
- Runtime metrics 唯一拥有 admission、协调租约、Provider attempt、quota operation、request recovery 和 background Responses 指标；异常终态日志使用稳定 `event`，只含封闭 outcome/operation/Provider kind/resource domain/count。Caddy 拒绝公网 `/metrics`，外部 Prometheus 从 backend 网络逐实例抓取。
- 管理信息架构只保留：Provider、模型、凭据、发布版本、用户、邀请、额度、Key、用量和 Playground。

## 持久与恢复边界

- PostgreSQL 保存 logical request、attempt、execution claim、reservation、usage 和后台 response 状态。
- Valkey 租约带 TTL；持有进程死亡后容量可恢复，但排队位置不持久化。
- 客户端排队时取消不会创建持久请求；接受后取消按是否已发送分别释放、结算或保持 uncertain。
- 流输出后失败不能拼接第二个 Provider 响应。
- 429/临时错误更新凭据冷却；管理员停用优先于自动健康。
- 恢复和取消操作幂等，fencing 阻止过期执行者覆盖新 owner。

## 安全边界

- Provider secret 使用版本化 AEAD；Gateway Key、邀请码、会话和 CSRF 使用带独立 pepper 的摘要。
- 自定义上游 URL 在连接与重定向阶段执行 SSRF、DNS 解析与地址范围检查。
- cookie、CSRF、权限、资源上限、日志脱敏和 secret canary 进入验证链。
- 普通日志与审计只记录非正文事实；系统不存储请求或响应正文。
- 主密钥轮换和 PostgreSQL 备份恢复必须使用显式、可验证、可回滚的运维命令，不能混入普通启动。
- 成员密码恢复由管理员+CSRF+idempotency mutation 原子更新 hash、撤销成员全部 session 并审计；管理员批量撤销自己的其他 session 时保留当前 session。唯一管理员锁定只由带确认开关与 password file 的离线 dbtool 恢复。
- Gateway Key replacement 在创建事务内锁定旧 Key，继承 owner、模型范围与到期事实，新旧 Key 形成显式重叠窗口；旧 Key 只在客户端切换确认后走独立幂等撤销。
- Linux 备份由 root oneshot 每 6 小时生成 PostgreSQL custom dump 与配置快照，再由固定 Restic 镜像写入加密远端 backend；明文 staging 无论成功失败都清理。repository/password/远端凭据与被备份的 `/etc/llmgateway` 分离，保留 7 daily、5 weekly、12 monthly。
- 灾备只恢复到空目录和新数据库，目标 RPO 6 小时、RTO 2 小时。恢复后的运行 secret 为 `root:65532 0640`，Valkey 固定 UID 999/GID 65532，Gateway 固定 UID/GID 65532；切流前必须重新证明管理员/成员边界、revision、Key、账本和 Provider 解密。
- Costing 独立拥有不可变 `model_price_versions`、幂等价格 mutation 和管理员聚合；model/adapter/router 不拥有价格。`requests` 在 quota acceptance 同一事务保存价格版本、ISO 币种和 input/output rate snapshot，settle/compensate 以整数 nanos 向上取整并检查溢出，release/uncertain 保持成本为空。价格变更不修改历史请求。

## 容量基线

当前目标 profile 面向 300 个已注册受控用户、约 20%（60 人）持续活跃。正式本机证据运行在 Windows amd64、32 逻辑处理器与 Docker Linux 15.5 GiB 可用内存上，使用两个 Gateway、PostgreSQL 18.4 和 Valkey 9.1.0；它是已验证基线，不是对其他主机、外部网络或真实 Provider 的无条件 SLA。

| 事实 | 已验证结果 |
| --- | --- |
| 15 分钟混合稳态 | 80,499/80,499 完成；p50 70 ms、p95 1.056 s、p99 1.060 s、首字节 p95 79 ms |
| 混合比例 | 65% 短 Chat、15% 短流、10% 约 1 秒流、5% 工具/reasoning、5% background Responses |
| 长连接 | 60 个独立用户各 600 个 SSE 事件、约 30.36 s，60/60 完成 |
| 300 人同步突发 | 178 成功、122 个带恢复信息的受控 429；所有用户获得终态 |
| 资源 | PostgreSQL client pool 为 24+24、观测连接 1；Valkey p95 1.09 ms；Gateway RSS 峰值 260 MiB、最终 140 MiB；goroutine 26→1,074→26 |
| 崩溃 | 实例一的 128 条已提交流全部中断并恢复为 uncertain hold；实例二先受共享租约阻塞，再在 TTL 到期后恢复流量 |

容量 owner 的默认 RPM 为 global 12,000，resource domain/model/Provider 各 9,000；user 600、Gateway Key 300 和未知 credential 60 保持保守。验收拓扑每实例使用 PostgreSQL `max=24/min=4`、本地 active 256、每用户 active 16、队列 512/30 s、租约 10 s 和 16 个 background worker。真实 credential、entitlement 和管理员配置的 RPM/TPM/并发继续做更窄的硬限制，不能因网关 profile 提高而被绕过。

持续 steady 429、首字节 p95 接近 2 s、普通请求 p99 接近 5 s、Valkey p95 接近 25 ms、RSS 持续超过 512 MiB，或数据库池到顶并同时出现 acquire wait/存储错误时触发扩容或降载。单次突发触发受控 429 不单独表示故障。发布候选可用同一入口把 `DurationSeconds` 提高到 43,200 做 12 小时 soak；当前完成证据为 15 分钟，外部 TLS/跨主机网络和真实 Provider 限额仍需部署及真实 Provider 验收分别证明。

## 交付拓扑

正式主拓扑固定为 Linux Docker Compose：Caddy 2.10.2 是唯一公开的 80/443 入口，两个 Gateway 在 edge/backend 网络之间运行，PostgreSQL 18.4 与 Valkey 9.1.0 只加入 internal backend。Gateway scratch 镜像以 UID/GID 65532、只读根文件系统、`cap_drop: ALL`、no-new-privileges 和显式 CPU/内存/PID/日志上限运行；PostgreSQL、Valkey、Caddy 和 Gateway 的生产引用全部要求 `@sha256`。

生产 secret 的唯一配置 owner 接受互斥的 `VAR` 或 `VAR_FILE`；正式部署只使用 file source。Compose secrets 分别挂载数据库 DSN、Valkey password/ACL、主密钥 ring 和三种 pepper/hash secret，环境、进程参数和镜像层只出现文件路径。Gateway `--check-config` 在连接存储前完成类型、长度和生产约束校验；production 默认不自动迁移，`/llmgateway-dbtool -action up` 是独立 migration job。

Caddy 以 readiness 主动检查两个 Gateway，SSE 使用 immediate flush 和 24 小时上限；Gateway 只信任 Caddy 固定内部地址提供的首个 `X-Forwarded-For`。Linux systemd unit 负责 Docker 依赖、自启动与有序停止；Windows amd64 使用同一 `webembed` 二进制的原生 SCM handler、虚拟服务账户、Event Log、延迟自启动和两次有界失败重启。Windows 服务停止映射到既有 30 秒 HTTP graceful shutdown。

升级 owner 先生成并校验 PostgreSQL custom dump、记录 Goose version，再逐实例替换。应用失败且 migration version 未变化时可回到旧 digest；version 已变化时禁止 image-only rollback，必须把 dump 恢复到新库、核验后切换 database URL file。隔离验收使用 Caddy internal CA，只证明 TLS 拓扑和代理合同；正式 DNS、公网证书、生产主机与镜像签名仍由发布环境提供。
