# LLMGateway 产品与系统规格

`spec.md` 是当前产品事实、系统边界、技术架构和运行拓扑的唯一规格。`dev.md` 规定如何开发以及恶劣路径怎样才算通过，`plan.md` 只记录当前任务、证据和外部未完成项。

## 1. 产品定位

LLMGateway 是面向约 200～300 名受控用户的中小微企业级多 Provider LLM 网关。产品以模块化单体交付，在聚焦的功能范围内保持完整的安全、并发、持久化、恢复和运维边界。

产品只有两个核心：

1. 多 Provider 统一 API：在官方合同和服务条款内最大化每条合法凭据的 RPM、TPM、并发和周期额度利用率；管理员管理 Provider、模型和上游凭据，发布统一目录，成员用一个 Gateway Key 调用已授权模型。模型/能力/资源域/启停/冷却先硬过滤，再按管理员 priority 和同级 weight 路由；429/503 只在明确安全的发送边界内有界退避或切换，未知副作用和已提交流不盲目重放。
2. 用户与管理员：管理员邀请、审核、停用成员，分配模型权限、额度、RPM、TPM、并发和 Key；成员只管理自己的 Key 并查看自己的用量。

双核心同时受三条横切原则约束：

- 用户友好：管理端和成员端把唯一内核事实投影成自然任务，让首次使用者从当前状态直接看见可执行的下一步；页面不拥有第二套调度、身份、额度或健康规则。
- 安全：身份、权限、secret、账本、网络、资源、供应链、部署和恢复均默认最小权限、可审计且失败关闭。
- 快速迭代：长期测试只保护稳定业务结果、公共合同、持久状态和高风险不变量，不冻结菜单、标题、按钮、文案、布局、文件名或内部实现。新 Provider、新模型和新运营投影可以快速加入，而不复制内核或重写无关测试。

最低生产地基与双核心同等重要，不因缩小功能范围而降低质量；三条横切原则也不构成额外业务平台。

### 稳定内核与快速迭代表面

LLMGateway 是需要持续适配模型、Provider 和运营方式的 AI 基础设施，必须保持快速迭代。稳定内核只包括公共协议与 Provider 边界、合法资源路由，以及管理员治理成员、权限、额度和 Key；PostgreSQL 账本、安全、并发、幂等、中断与灾备是支撑这些内核投入生产的必要地基。它们发生变化时必须同步修改合同、实现、恢复和测试。

管理端页面、信息架构、菜单、标题、按钮数量与位置、说明文案、主题、颜色、对齐、Dashboard 组合和移动投影属于高频变化表面，只消费并操作唯一内核事实，不拥有第二套业务规则。新增或删除这些投影不能要求兼容旧 UI，也不能因为既有测试而冻结设计；稳定业务结果、权限边界和公共合同未变时，相关测试不应变化，反之说明测试绑定了偶然结构，应删除或收敛。

产品在“玩具”和“大平台”之间保持明确中型边界：面向约 200～300 名受控用户，纳入范围的能力必须具备生产级安全、持久化、并发、恢复、观测、部署与灾备闭环；不为没有真实需求的组织层级、工作流、支付、万能设置或页面排列建立平台化抽象和测试矩阵。扩展新功能优先沿两个核心增加事实或投影，不复制业务内核。

### 用户与价值

- 管理员：集中管理上游资源、成员访问、额度、配置发布、成本和运行健康。
- 成员：使用一个 Base URL 和自己的 Gateway Key 调用已授权模型，不接触上游 secret。
- 调用方：通过标准 SDK 或 HTTP 使用稳定、可解释的公共合同。

收入来自统一接入、稳定运营、权限治理和模型资源整合所提供的服务价值；成本来自上游模型、基础设施和运维。网关账本记录可审计 usage、额度消耗和冻结的上游采购成本，但不拥有客户售价、充值、收款、合同、毛利、开票或结算流程。

### 不在当前范围

- 批量注册、接码、养号、绕过风控、规避上游条款或突破额度。
- 在线充值、支付、开票和面向公众的自由注册。
- Kubernetes、多地域主动高可用，以及 Linux arm64/macOS 正式服务。
- 图像、视频、语音、Embedding、Rerank 等独立协议；当前公共合同聚焦文本生成。

“免费”只表示合法可用的免费模型、套餐或额度，不承诺上游永久免费、无限量或始终可用。

## 2. 用户主旅程

```text
首次创建管理员
  -> 添加 Provider、模型和上游凭据
  -> 捕获、校验并发布配置
  -> 配置模型价格
  -> 邀请并审核成员
  -> 分配模型、额度、速率与 Gateway Key
  -> 成员从 Gateway Key 就地测试或通过统一 API 调用
  -> 管理员核对 usage、成本、健康与审计
  -> 调整额度、替换/撤销 Key 或发布新配置
```

- 全新系统由第一位访问者通过一次性 setup 创建唯一的首位管理员；setup 只接收邮箱，服务端生成高熵初始密码并在成功页面只展示一次，不存在默认账号或密码。
- setup 完成后入口关闭。后续成员必须持管理员创建的一次性邀请码注册，并由管理员审核后激活。
- Provider Key 只由管理员作为上游凭据维护；Gateway Key 是分配给调用方的下游凭据，两者不能混用。
- 一次性邀请码和 Gateway Key 完整值只在创建响应或同一幂等操作恢复时显示。

## 3. 公共 API 与 Provider 合同

- OpenAI-compatible `GET /v1/models`、`POST /v1/chat/completions` 和 `POST /v1/responses`。
- Chat Completions 与 Responses 支持非流式、流式、工具调用、reasoning 和 usage；无法无损表达的能力在发送前明确拒绝。
- Public API 拥有客户端可见协议，Canonical Model 拥有内部统一语义，Provider Adapter 拥有厂商 wire 差异。
- 当前专用 adapter 为智谱 GLM、Agnes 和 Google Gemini，同时保留明确能力子集的通用 OpenAI-compatible adapter。
- 新增 Provider 优先复用通用兼容合同；只有官方合同与隔离 wire 证明存在无法无损表达的差异时才增加专用 adapter。
- Provider kind、展示名称、builder、权威合同 URL、合同快照日期、现场验证日期、参考模型、现场能力和 `verified/degraded` 状态由唯一 catalog definition 拥有。请求、探测、写入校验、管理端和兼容矩阵只投影这份事实。
- 模型名、价格、能力、错误、RPM/TPM 和额度是易变外部事实；接入或修改时必须重新依据官方资料和真实隔离请求核验，未知余额不得伪造成剩余额度。
- reasoning 模型同时声明 `toggle`、`effort` 或 `hybrid` 控制 profile。通用 adapter 只按 profile 投影 `enable_thinking` 或 `reasoning_effort`，不按 Provider 名称在请求链散落分支。

### 2026-07-22 现场兼容事实

以下矩阵记录该日隔离验收，不构成上游永久 SLA：

| Provider | kind | 现场模型 | 已证明合同 |
| --- | --- | --- | --- |
| Agnes | `agnes` | `agnes-2.0-flash` | models、chat、stream、tools、thinking、usage、取消与未知边界 |
| 智谱 GLM | `zhipu` | `glm-5.2` | models、chat、stream、tools、reasoning、usage、结构化 quota 与 priority 接管 |
| Google Gemini | `gemini` | `gemini-3.5-flash` | models、工具调用、thought signature 回放、reasoning、usage 与结构化错误 |
| 硅基流动 | `openai-compatible` | `Qwen/Qwen3.5-9B` | models、chat、Responses、stream、tools、reasoning、usage 与标准 SDK |

标准客户端现场版本为 OpenAI Go `v3.44.0` 与 Python `openai==2.46.0`。

## 4. 配置、路由与资源域

- 管理员维护 Provider、模型、凭据和模型绑定；上游 secret 使用版本化 AEAD 加密保存。
- 捕获操作在 PostgreSQL 事务内复制 Provider、模型、凭据非秘密字段和路由绑定，生成带 checksum 的不可变 catalog revision。
- 发布以 expected active version 做并发控制，原子切换 PostgreSQL 中唯一 active revision；数据面只读取 active revision，Valkey 不保存第二套配置。
- 回滚把已有 revision 重新设为 active，并递增 active version。
- 路由先过滤模型、资源域、能力、启停、授权、冷却和本次已尝试凭据，再选择数值最小的管理员优先级，并在同级凭据中按正权重选择。
- RPM、TPM 与并发由 Valkey 原子短期租约和限流计数拥有；用户额度由 PostgreSQL 原子账本拥有，不能混成虚构综合评分。
- 免费与专业资源域严格隔离。免费资源不可用时返回可理解错误，不自动消耗专业或付费资源。
- 本地 admission 保证每用户 FIFO、用户间轮转和每用户并发上限；多实例共享 Valkey 容量，不承诺进程重启后保留绝对排队顺序。最大排队时间覆盖 admission 与首次发送前的执行并发等待；只重试并发租约，RPM/TPM 只原子消耗一次。
- 只在已知安全发送边界内有限重试。上游副作用未知、响应流已提交或请求不可安全重放时不得换凭据盲目重放。

## 5. 身份、权限、Key 与额度

- 系统只有 `administrator` 和 `member` 两种角色；导航按 capability 生成，服务端始终独立执行授权。
- 已登录用户可以验证当前密码后更换自己的密码；更新在 PostgreSQL 事务内保留当前会话、撤销其他会话并写审计。
- 管理员可以管理 Provider、模型、凭据、发布配置、用户状态、授权、额度和任意用户的 Gateway Key。
- 成员只能查看自己的授权、额度、用量和 Key，并更换或撤销自己的 Key。
- Gateway Key、邀请码、会话和 CSRF 只保存带独立 pepper 的摘要、必要前缀和不含 secret 的 mutation 结果。
- 活动 Gateway Key 可以创建同 owner、同模型范围和同到期时间的 replacement。新旧 Key 在迁移窗口同时有效；确认客户端切换后，旧 Key 再走独立幂等撤销。
- 配额请求先在 PostgreSQL 原子预留，再按上游权威 usage 结算；并发竞争不得超扣、重复结算或漏结算。
- Provider 模型价格使用只增不改的生效版本。币种是三位 ISO 代码，输入/输出费率和请求成本使用整数 currency nanos，不用浮点数。
- 请求接受事务冻结当时价格；缺价在发送前失败，已知 usage 在结算事务写入成本，unknown/uncertain 不伪造金额。
- 只有管理员能读取采购价格和按用户、套餐、模型、Provider、资源域、币种聚合的成本；成员个人 usage 不包含采购价。

## 6. 失败、恢复与安全不变量

- PostgreSQL 是身份、配置、请求、attempt、额度、usage、mutation、成本和审计的持久事实 owner。
- Valkey 只拥有可过期、可重建的限流计数和并发租约；协调不可用时 fail closed，不能绕过共享容量。
- 管理会话使用 HttpOnly cookie、独立 CSRF token、过期和撤销；停用用户会阻止后续管理和公共调用。
- 管理员可以幂等重置成员密码并撤销成员全部会话；撤销自己的其他会话时保留当前会话。唯一管理员锁定只通过显式确认、password file 和 system audit 的离线命令恢复。
- 自定义 Provider URL 在连接和重定向阶段防御 SSRF、DNS 重绑定与内网访问，默认不允许内网和回环地址。
- 普通日志、错误、指标、审计和浏览器持久状态不得保存 Provider secret、Gateway Key、邀请码、请求或响应正文。
- HTTP body、响应字节、队列、并发、连接、流时长和超时都有显式上限。
- 客户端取消、断连、partial stream、429/5xx、协调失败、进程强杀与重启必须留下可解释终态；未知上游副作用保持 `uncertain`，不自动重放。
- 流输出后失败不能拼接第二个 Provider 响应；恢复、取消、回调和清理必须幂等，fencing 阻止过期执行者覆盖新 owner。

## 7. 技术架构

| 层 | 当前选择 | 职责 |
| --- | --- | --- |
| 服务端 | Go 1.26 模块化单体 | 公共 API、管理 API、后台恢复和生产前端交付 |
| HTTP | `net/http` + `chi` | 请求上限、取消、流式生命周期和中间件 |
| 持久化 | PostgreSQL 18 | 持久事实的唯一 owner |
| SQL | `pgx` + `sqlc` + 基线 migration | 显式事务与确定性生成类型 |
| 协调 | Valkey 9 | RPM/TPM、并发租约和跨实例短期容量 |
| 管理端 | React 19 + TypeScript + Vite 8 | 生产构建嵌入 Go 二进制 |
| 前端状态 | TanStack Router/Query/Table | 路由、服务端 cache 和表格 |
| UI | Radix 原语、Lucide、CSS tokens | 可访问、桌面与移动可操作 |
| 运行证据 | 结构化日志 + Prometheus | request ID、稳定错误类别、容量与恢复结果 |

模块化单体适合当前 200～300 名受控用户：部署和恢复边界清楚，各领域仍按事实 owner 隔离。只有真实出现独立扩容或发布需求时才拆服务。

### 运行拓扑

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

- 单个 Go 进程提供管理 API、公共 API、恢复 worker 和嵌入式生产前端。
- PostgreSQL 故障时不能接受需要持久事实的新工作；Valkey 故障时不能绕过共享容量。
- 多实例共享 PostgreSQL 与 Valkey。请求执行通过持久 claim/heartbeat 和过期租约协调，强杀后由存活或重启实例恢复。

### 模块 owner

```text
internal/
  publicapi/       /v1 鉴权、协议响应与流式边界
  protocol/        OpenAI-compatible wire 解析与呈现
  canonical/       消息、工具、reasoning、usage 与错误语义
  providers/       Provider adapter、能力与错误分类
  identity/        用户、角色、会话、邀请、Gateway Key 与模型授权
  registry/        Provider、模型、凭据、绑定、健康与冷却
  configuration/   catalog revision、发布、回滚与 active version
  admission/       本地每用户 FIFO、用户轮转与并发上限
  coordination/    Valkey 原子限流与租约
  routing/         硬资格、管理员优先级与同级权重选择
  quota/           额度预留、结算、补偿与 usage
  requestflow/     接受、执行、发送、流式、结算与恢复编排
  execution/       持久执行 claim 与 fencing
  responses/       后台 Responses 状态机
  security/        AEAD、摘要、SSRF 与网络策略
  controlapi/      管理 HTTP 合同与权限边界
  store/           PostgreSQL/Valkey adapter 与 sqlc 消费者
  app/             配置校验、依赖装配与进程生命周期
```

调度不解析 Provider wire，adapter 不判断用户额度，HTTP handler 和 UI 不重算领域状态，领域模块不反向依赖具体交付层。

### 请求数据流

```text
校验 Gateway Key 与模型授权
  -> 本地公平 admission + Valkey 共享容量
  -> PostgreSQL 原子额度预留并持久接受请求
  -> active catalog revision 解析模型与候选凭据
  -> 硬资格过滤 -> 最小 priority -> 同级 weighted choice
  -> 取得 Provider/credential/用户/Key 多维租约
  -> 解密最小必要 secret，SSRF 安全客户端发送
  -> 非流/流式响应归一
  -> 权威 usage 结算，或按发送事实释放/保持 uncertain
```

### 管理信息架构

管理端只保留 Provider、模型、凭据、配置版本、用户、邀请、额度、Gateway Key、用量和成本；Provider API Key 与 Gateway Key 的测试分别就地完成。Runtime metrics 唯一拥有 admission、租约、Provider attempt、quota、请求恢复和后台 Responses 指标；Caddy 对公网拒绝 `/metrics`，Prometheus 从 backend 网络逐实例抓取。

## 8. 容量基线

当前 profile 面向 300 个已注册受控用户、约 60 人持续活跃。2026-07-22 的 900 秒正式本机证据运行在 Windows amd64、32 逻辑处理器和 Docker Linux 15.5 GiB 可用内存上，使用两个 Gateway、PostgreSQL 18.4 与 Valkey 9.1.0。它是已验证基线，不是对其他主机、外部网络或真实 Provider 的无条件 SLA。

| 事实 | 已验证结果 |
| --- | --- |
| 15 分钟混合稳态 | 79,892/79,892 完成；p50 75 ms、p95 1.062 s、p99 1.067 s、首字节 p95 85 ms |
| 长连接 | 60 路各约 30.4 s，60/60 完成，首字节 p95 162 ms |
| 300 人同步突发 | 181 成功、119 个带恢复信息的受控 429；全部获得终态 |
| 热点限流 | 8 成功、24 个受控拒绝，未突破硬限制 |
| 资源 | PostgreSQL pool 24+24、观测连接 1；Valkey p95 1.13 ms；Gateway RSS 峰值 259 MiB、最终 158 MiB；goroutine 26→1,107→26 |
| 账本 | 80,309 个最终请求；80,162 settled、143 released、4 个预期 uncertain |

容量 owner 默认 RPM 为 global 12,000，resource domain/model/Provider 各 9,000；user 600、Gateway Key 300、未知 credential 60。真实 entitlement、credential 和管理员配置的 RPM/TPM/并发继续做更窄硬限制。

持续 steady 429、首字节 p95 接近 2 秒、普通请求 p99 接近 5 秒、Valkey p95 接近 25 ms、RSS 持续超过 512 MiB，或数据库池到顶并出现 acquire wait/存储错误时触发扩容或降载。单次突发产生受控 429 不单独表示故障。当前证据为 15 分钟；12 小时 soak、外部 TLS/跨主机网络和真实 Provider 限额仍由发布与目标环境验收证明。

## 9. 正式交付、备份与恢复

- 正式主拓扑是单台 Linux 主机上的 Docker Compose：Caddy 是唯一公开 80/443 入口，两个 Gateway 位于 edge/backend 网络，PostgreSQL 与 Valkey 只加入 internal backend。
- Gateway scratch 镜像以 UID/GID 65532、只读根文件系统、`cap_drop: ALL`、no-new-privileges 和显式资源/日志上限运行；生产镜像引用全部要求 `@sha256`。
- production secret 只通过显式 file source 进入 Compose secrets；环境、参数和镜像层只出现路径。Gateway 在连接存储前完成配置校验，生产不自动 migration。
- 独立 migration 成功后才逐实例替换。应用失败且 migration version 未变化时可回到旧 digest；version 已变化时禁止 image-only rollback，必须恢复备份到新库、核验后切换 database URL file。
- 正式主机每 2 小时调度 PostgreSQL custom dump 与恢复配置进入加密异地 Restic 仓库，失败 10 分钟后有界重试，最近恢复点不得超过 6 小时；保留 7 daily、5 weekly、12 monthly，备份控制凭据与被备份目录分离。
- 灾备只恢复到空目录和新数据库，目标 RPO 6 小时、RTO 2 小时；切流前重跑管理员、成员、revision、Key、额度、账本和 Provider 解密主旅程。
- Windows amd64 使用同一 `webembed` 二进制的原生 SCM handler、虚拟服务账户、Event Log、延迟自启动和两次有界失败重启；正式用户流量仍经过受信任 TLS 代理。
- 正式域名、DNS、公网证书、生产主机、镜像仓库、外部监控、通知渠道、异地仓库和发布签名身份属于目标环境事实。缺少现场证据时必须明确标记未完成。

具体安装、升级、回滚、备份与恢复命令由 [deploy/README.md](deploy/README.md) 唯一维护。

## 10. 完成不变量

- 当前没有必须保留的正式生产数据；首次发布前的不兼容 schema、API、配置和事件变化直接重建当前基线，不维护双读写或过渡 migration。
- 每个事实只有一个 owner，handler、UI、adapter 和测试不保存第二套业务事实。
- 阶段可以缩小功能范围，但纳入范围的成功、错误、并发、中断、恢复、安全、可观测性、测试、文档和目标环境验证必须闭环。
- 最终验收连接真实 Go、PostgreSQL、Valkey、生产构建前端和目标 Provider，并以有头 Chromium 与标准 SDK 覆盖管理员、成员、取消、刷新、重登、强杀、升级和恢复。
- LLMGateway 使用 MIT License；参考仓库只研究机制与失败经验，不复制许可证不兼容源码。
