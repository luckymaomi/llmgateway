# LLMGateway 从零到生产可用

> 本文件是当前正式实现任务的唯一执行合同。它覆盖从空代码仓库到可供小规模团队稳定使用、并具备水平扩展边界的完整交付。执行前由 owner 审阅；审阅通过后按生产级切片推进，checklist 随事实即时更新。

## 需求文档

### 用户与场景

- 管理员合法持有多个 Provider 的 API Key，希望统一管理模型、凭据、免费额度、付费资源、限流和故障状态。
- 初期服务几十名受邀用户；用户使用一个 LLMGateway Base URL 和自己的网关 Key 调用已授权模型。
- 用户必须通过邀请码注册并经管理员审核。管理员可以停用用户、撤销 Key、授权模型、分配额度或套餐。
- 免费资源池在用户之间公平共享；免费池耗尽、冷却或不可用时明确返回状态并等待恢复，绝不自动消费付费资源。
- 付费资源由管理员统一购买和配置，用户消费 LLMGateway 内部额度或套餐；首期线下结算、管理员人工入账，不接支付接口。
- 管理员需要保留脱敏请求事实和受控请求内容，以排查错误与恶意使用；普通日志不能泄露正文或密钥。
- 人类不仅通过 Agent 调用，还需要在 Web Playground 直接测试文本、工具调用、reasoning 和流式响应，并看到真实进度与错误阶段。

### 要解决的问题

- 统一 OpenAI-compatible 公共协议，隔离不同 Provider 的鉴权、字段、流事件、工具调用、reasoning、usage 和错误差异。
- 管理多 Provider、多模型和多凭据，按能力、资源域、健康、限额、当前负载、优先级和会话粘性选择上游。
- 在全局、用户、网关 Key、模型、Provider 和凭据层实施并发、RPM、TPM、额度和公平排队。
- 对 429、临时 5xx、超时和网络错误实施有界退避、冷却、熔断和恢复，同时保护流式提交与未知副作用边界。
- 用可审计账本完成额度预留、权威 usage 结算、本地估算、补偿、人工调整和套餐期限管理。
- 在客户端断连、重复请求、进程强杀、主机重启和数据库/Valkey 短暂故障后保持事实可解释、可恢复且不重复扣费。
- 以单机低运维成本起步，同时保证 PostgreSQL 权威状态和 Valkey 临时协调不阻塞后续多实例扩容。

### 当前任务范围

- Go 模块化单体、PostgreSQL、Valkey、React 管理端和生产构建/发布链。
- 邀请注册、管理员审核、角色权限、可撤销会话、网关 Key、用户模型授权。
- Provider、模型、能力、凭据、资源域、固定代理、配置草稿/发布/回滚。
- 通用 OpenAI-compatible、智谱 GLM、DeepSeek 官方和 Agnes adapter。
- 公共模型列表、Chat Completions 与 Responses 的明确兼容子集。
- 多凭据调度、公平 admission、限流、短时排队、会话粘性、冷却、熔断、有限重试和故障切换。
- 免费资源域、专业资源域、Token Plan、Coding Plan、人工额度分配和 append-only 账本。
- 请求、attempt、usage、错误、健康、审计、告警和受控内容留存。
- 管理工作台与用户 Playground。
- Windows、Linux、macOS 常见 x64/ARM 构建，Docker 镜像、备份恢复、优雅停机和多实例一致性验证。

### 可验收完成标准

- 新环境可以按文档初始化、创建管理员、邀请用户、接入 Provider/模型/凭据、发布配置并签发网关 Key。
- 授权用户可使用标准客户端完成模型列表、非流式/流式聊天、Function Calling、reasoning、多轮工具回放和 Responses 调用。
- GLM、DeepSeek、Agnes 和通用 OpenAI-compatible 均有官方合同快照、确定性 wire 测试和隔离的真实 API 验收结果。
- 多用户并发下公平排队、额度隔离、免费/付费隔离、限流、重试、冷却、熔断和粘性路由行为可解释且有确定性测试。
- 重复、取消、断连、超时、部分流、未知发送边界、进程强杀、重启和存储故障不会产生静默成功、永久 running、重复请求或重复结算。
- 管理端覆盖所有首期管理与 Playground 路径，并对提交、执行、等待、成功、失败和恢复显示真实状态。
- 唯一完整验证命令通过；目标平台构建、数据库备份恢复、单机真实链、多实例协调链和安全检查均有记录。
- README、spec、architecture、dev、API 文档、部署文档和实现讲同一个当前事实。

## 当前事实

### 已确认的仓库与运行事实

- 当前分支为 `master`，基线提交 `f17ce79` 已推送到 `origin/master`。
- 仓库目前只有产品/架构/开发文档、参考仓库和开发基础设施；没有 `go.mod`、Go 源码、前端工程、数据库 schema、公共 API、管理端或业务测试。
- 根目录不存在旧生产实现、旧 API、旧 schema 或必须保留的正式生产数据；首次生产发布前，不兼容变化直接重建当前基线。
- `architecture.md` 已确认 Go 1.26.5+、模块化单体、`net/http` + `chi`、PostgreSQL 18、`pgx` + `sqlc` + `goose`、Valkey 9、React 19 + TypeScript + Vite 8、TanStack 与 OpenTelemetry/Prometheus 方向。
- 本机已验证 Go 1.26.5、Node 22.19.0、pnpm 10.33.0、Docker 29.6.1 和 Docker Compose 5.3.0。
- `postgres:18.4-alpine` 与 `valkey/valkey:9.1.0-alpine` 当前运行健康，分别绑定 `127.0.0.1:15432` 与 `127.0.0.1:16380`。
- 停止并重建容器后 PostgreSQL 与 Valkey 探针可以恢复；临时探针已经清理。
- `ref/repos` 中的 LiteLLM、New API、Portkey Gateway、Sub2API、Uni API 仅用于机制研究并被 Git 忽略，不是代码来源。

### 已核验的外部合同

核验日期：2026-07-19。模型、价格、免费额度、RPM/TPM 和错误细节属于易变事实，进入对应实现切片时必须重新核验，不以本计划永久冻结。

- OpenAI 官方当前分别定义 [Models](https://developers.openai.com/api/reference/resources/models/methods/list)、[Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)、[流事件](https://developers.openai.com/api/docs/guides/streaming-responses) 和 [Function Calling](https://developers.openai.com/api/docs/guides/function-calling) 合同。Chat Completions 与 Responses 的请求/流事件不是同一种 wire，必须分别拥有公共协议 presenter/parser。
- 智谱官方 [Chat Completions](https://docs.bigmodel.cn/api-reference/模型-api/对话补全) 使用 Bearer、`/api/paas/v4/chat/completions`、SSE `[DONE]`、`tools/tool_calls`、`thinking` 和 usage；[工具调用](https://docs.bigmodel.cn/cn/guide/capabilities/function-calling) 与 [保留思考](https://docs.bigmodel.cn/cn/guide/capabilities/thinking-mode) 要求模型能力声明和工具轮次 reasoning 回放。
- DeepSeek 官方使用 OpenAI 请求格式和 `https://api.deepseek.com`；[Function Calling](https://api-docs.deepseek.com/guides/function_calling) 与 [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/) 明确 `reasoning_content`、工具调用回放和模型参数差异。
- Agnes 官方模型目录位于 [AgnesAI-Labs/AgnesAI-Models](https://github.com/AgnesAI-Labs/AgnesAI-Models/blob/main/MODEL_CATALOG.md)。实施时以官方目录和真实隔离请求核验本轮接入模型的协议、流式、工具、reasoning、usage 与错误合同。

### 未知项及验证方式

- 每个 Provider 当前可用模型、免费额度、RPM/TPM、并发、余额查询能力：实现切片开始时查看官方文档/控制台，并用隔离账号探测；无法权威查询时保存 `unknown`。
- Agnes 当前合同的完整错误、工具、reasoning、usage 和响应字段：以官方仓库、平台文档和隔离请求生成 wire fixture；未知字段不得猜测。
- 通用 OpenAI-compatible Provider 的具体差异：只实现明确标准子集，接入时由管理员声明能力；无法无损转换的参数明确拒绝。
- 生产监听、域名、TLS 证书、邮件发送和告警出口：实现为类型化配置和可验证适配器；没有 owner 配置时保持禁用，不伪造成功。
- 正式生产数据边界：只有 owner 明确宣布后才成立；此前 schema 直接重建，成立后切换到一次性前向迁移纪律。

### 工作区保护边界

- 基线提交已推送；本计划创建后保持为待 owner 审阅的工作区改动。
- 不读取或迁移 Kitty 的真实 `.env`、API Key、日志、请求正文或个人数据。
- 不把参考仓库源码、许可证不兼容代码或生成产物复制进主干。
- 浏览器自动化只处理可验证的结构与业务交互；颜色、间距、品牌感和整体审美仍由 owner 验收。

## 失败证据

当前系统尚未实现，以下结果是计划执行前的可观察失败事实：

- 根目录没有 `go.mod`，无法构建或运行网关二进制。
- 没有数据库 migration，PostgreSQL 中不存在用户、Provider、凭据、请求、账本和审计事实。
- `GET /v1/models`、`POST /v1/chat/completions` 和 `POST /v1/responses` 均不存在。
- 没有管理员初始化、邀请注册、登录、网关 Key、模型授权、额度或套餐入口。
- 没有 Provider 配置发布、凭据加密、调度、限流、公平排队、重试、冷却、熔断或 usage 结算。
- 没有 Web 管理端、用户 Playground、请求日志、健康看板或告警。
- 现有 `scripts/verify-environment.ps1` 只能验证开发环境，不能验证任何产品行为。

计划完成前，上述任一公共路径仍不存在、返回占位成功、只在内存成立或无法恢复，都属于失败，不允许用文档、mock 或 UI 假数据掩盖。

## 最终目标

### 生产级终局

LLMGateway 以一个 Go 服务承载公共数据面、管理 API 和嵌入式 React 管理端，以 PostgreSQL 保存权威事实、Valkey 保存可重建协调状态。它可以在单机上低成本运行，也可以启动多个等价实例共享状态。

系统完成以下闭环：

```text
管理员初始化 -> 邀请/审核用户 -> 注册 Provider/模型/凭据 -> 校验并发布配置
-> 用户获得模型授权/额度/套餐和网关 Key
-> 请求鉴权 -> 公平 admission -> 原子额度预留 -> 路由/凭据选择
-> 限流/并发许可 -> Provider 发送 -> 流/响应归一 -> usage 结算/补偿
-> 请求事实、attempt、指标、审计和用户结果
```

### 必须保持的不变量

- 每个事实一个 owner；PostgreSQL 是权威事实，Valkey 丢失后可重建。
- 免费资源域与专业资源域在授权、路由、预留、结算和错误路径上严格隔离。
- 上游凭据不返回客户端；网关 Key、密码、会话和上游凭据使用各自正确的摘要或加密方式。
- 额度先原子预留再发送，最终按权威 usage 或明确估算结算；重试 attempt 不重复扣除同一逻辑请求。
- 流一旦向客户端提交，不透明重试或切换；未知发送/副作用边界持久表达为 `uncertain`。
- 配置只通过完整校验后的不可变发布版本进入数据面；运行请求绑定实际读取的发布版本。
- Provider wire 差异只存在于 adapter/policy；调度、账本和 UI 不匹配厂商错误字符串。
- 排队、租约、取消、回调、清理、恢复和补偿有界且幂等；任何失败留下可行动证据。
- 模型能力、额度和费率不靠记忆或营销文案推断；来源、核验时间和 `known/estimated/unknown` 状态可见。
- 正式生产前直接切割错误设计；正式生产后只做一次性前向迁移，不建立永久兼容双轨。

## 不做范围

- 不管理 OAuth、网页登录态、订阅账号或自动刷新第三方登录令牌。
- 不提供批量注册、接码、养号、自动获取免费账号、绕过风控、突破额度或规避 Provider 条款的能力。
- 不接自助支付、在线充值、退款、发票、商城、推广返佣或公开售卖流程；仅实现可审计人工额度/套餐分配。
- 不允许用户自带上游 Key；首期上游凭据只由管理员管理。
- 不建设无目的轮换 IP 池；只支持管理员明确配置的直连、固定代理和备用出口，并实施 SSRF 防护。
- 不实现本地 LLM 下载、推理或 llama.cpp 管理。
- 本轮交付聚焦 Models、Chat Completions、Responses、工具调用、reasoning、流式和对应管理能力。
- 不宣称完整复刻 OpenAI 所有参数。支持矩阵外的字段返回稳定、可解释的 `unsupported_capability`，不静默丢弃。
- 不建立微服务、Kafka、服务网格、Kubernetes operator 或插件市场；没有事实需求前保持模块化单体。
- 不保留旧 API、旧 env、旧 schema、旧别名或过渡 migration。
- 不用截图替代业务交互验收，也不由 Agent 宣布视觉审美合格。

## 设计

### 事实 Owner

| Owner | 拥有 | 不拥有 |
| --- | --- | --- |
| Public Protocol | `/v1` 路由、鉴权入口、请求校验、响应/流事件、标准错误 | Provider wire、调度、额度 |
| Canonical Model | 消息、内容块、工具、reasoning、usage 与错误的统一语义 | HTTP、数据库、厂商字段 |
| Identity and Access | 用户、邀请、审核、角色、会话、网关 Key、模型授权 | 上游凭据、用量余额 |
| Provider Registry | Provider、模型、能力、端点、资源域、来源与配置版本 | 凭据密文、调度健康 |
| Credential Pool | 凭据密文、模型授权、固定出口、启停、健康、冷却 | 用户权限、账本余额 |
| Admission and Fairness | 有界队列、用户公平、优先级、并发许可与背压 | 上游选择、费用结算 |
| Routing and Scheduling | 模型别名、资源域、资格过滤、凭据评分、粘性 | 速率计数、Provider wire |
| Rate Limit and Resilience | Token bucket、超时、退避、重试、熔断、恢复 | 业务额度、错误文案 |
| Usage and Quota Ledger | 预留、权威/估算 usage、结算、补偿、人工调整、套餐期限 | Provider 余额猜测 |
| Configuration Publication | 草稿、完整校验、不可变版本、发布、生效与回滚 | 各模块内部规则 |
| Observability and Audit | request/attempt 事实、指标、trace、操作审计、受控内容留存 | 调度和账本规则 |

### 代码与依赖方向

```text
cmd/gateway
  -> transport/http + web embed
  -> application workflows
  -> domain owners
  <- provider/store/crypto/telemetry adapters
```

- 领域包定义稳定类型、端口和状态机，不依赖 HTTP、React、具体 Provider、pgx 或 Valkey。
- HTTP handler、数据库 repository、Provider adapter 和 UI 都是边缘接线。
- `map[string]any`/raw JSON 只允许在明确 passthrough wire 边界，进入核心判断前必须验证并归一。
- 生成代码由 OpenAPI/SQL/路由等明确源产生，生成物不可手改；完整验证检查漂移。
- 内部命名只表达领域职责；外部 `/v1`、官方模型名和 migration 序号不扩散成内部版本壳。

### 持久聚合与状态

首批聚合及关键状态如下，最终表结构由 sqlc query 和 migration 同时拥有：

- `User`：`pending_review -> active -> suspended`；删除采用可审计停用，敏感字段按保留策略清理。
- `Invitation`：`issued -> claimed -> approved/expired/revoked`；claim 与注册事务化，一码不可重复使用。
- `GatewayKey`：只展示一次明文，持久化前缀、摘要、owner、模型授权、有效期和撤销状态。
- `Provider/Model`：能力、外部模型 ID、资源域、来源、核验时间和发布版本。
- `Credential`：加密 payload、密钥版本、模型授权、出口、状态和持久健康事实；短期租约/计数在 Valkey。
- `ConfigurationRevision`：`draft -> published -> superseded`；发布使用乐观锁和单一 active revision。
- `GatewayRequest`：`queued -> admitted -> dispatching -> streaming/completed/failed/canceled/uncertain`；执行状态与结算状态分离。
- `ProviderAttempt`：每次选路、发送边界、HTTP/request ID、错误种类、usage、延迟和提交状态。
- `QuotaReservation`：`reserved -> settled/released/compensated/uncertain`；状态转换由唯一事务边界保护。
- `LedgerEntry`：append-only，类型包括 grant、reserve、settle、release、adjust、expire、refund/compensate；余额是可重建投影。
- `Entitlement`：Token Plan、Coding Plan、模型范围、资源域、时间窗口、并发/RPM/TPM 与额度。
- `AuditEvent`：操作者、动作、对象、前后摘要、request ID 和时间；密钥明文永不进入事件。
- `ContentRecord`：与普通请求日志分表、加密/访问控制、保留期、访问审计和删除状态。

所有 ID 使用不可预测稳定标识。时间点以 UTC 持久化；持续时间和截止时间显式分型。Token、字节、毫秒、请求数和货币最小单位在名称/类型中表达；账本金额不使用浮点数。

### 公共协议表面

首期公共数据面：

- `GET /v1/models`：只返回当前网关 Key 有权使用且已发布、可路由的模型别名和基础元数据。
- `POST /v1/chat/completions`：文本输入、非流/流、Function Calling、tool result、reasoning、usage 和标准错误。
- `POST /v1/responses`：文本/内容块、instructions、function tools、tool outputs、非流/流、reasoning 和 usage；托管工具明确拒绝。
- `GET/DELETE /v1/responses/{response_id}`、`GET /v1/responses/{response_id}/input_items`：只服务由 LLMGateway 持久化的 response；访问受原网关 Key owner 约束。
- `POST /v1/responses/{response_id}/cancel`：只对仍可取消的后台 response 生效；重复取消幂等。

公共错误使用稳定 `error.type`/`error.code` 驱动客户端行为，message 只用于展示。至少区分 invalid_request、authentication、permission、quota、admission_timeout、rate_limit、unsupported_capability、provider_configuration、provider_temporary、provider_permanent、stream_interrupted、uncertain、storage_unavailable 和 internal_invariant。

### Provider Adapter

- `openai-compatible`：Bearer、可配置 Base URL、显式模型/能力，支持标准 Chat Completions 子集；不携带厂商特判。
- `zhipu`：官方 endpoint、thinking、preserved reasoning、tool stream、usage 和错误/request ID 归一。
- `deepseek`：官方 OpenAI 格式、thinking/effort、工具轮次 reasoning 回放、无效采样参数处理和错误归一。
- `agnes`：按官方合同实现 adapter，并以明确 policy 承担本轮接入模型的工具、reasoning、usage 和错误差异。
- 每个 adapter 提供 capability descriptor、request builder、stream parser、response parser、usage parser、error classifier 和脱敏 wire fixture。
- Provider 连接测试分为不消耗模型 Token 的配置/认证探测（仅在官方提供安全端点时）和显式标注可能计费的真实生成测试；UI 必须告知差异。

### Admission、公平与调度

1. 请求先校验身份、模型权限、资源域和静态上限；非法请求不入队、不预留额度。
2. 合法请求进入按资源域隔离的有界 admission。每个用户 FIFO，活跃用户按加权公平次序获得许可；单用户不能用大量长连接占满全局槽位。
3. 多实例下 Valkey 原子维护全局/用户/Key/模型/Provider/凭据的短期 ticket、token bucket 和带 TTL 并发租约；租约失效可从 PostgreSQL 请求事实重建或清理。
4. 获得 admission 后在 PostgreSQL 原子创建请求事实与额度预留；预留失败不发送上游。
5. Scheduler 先做硬资格过滤：已发布模型、资源域、能力、凭据授权、active、未冷却、并发/RPM/TPM 可用、出口健康。
6. 同一会话优先使用仍合格的粘性凭据；失效时记录逃逸原因再重选，不假设上游保存上下文。
7. 候选评分使用管理员优先级、已知额度、当前负载、近期成功/错误、TTFT/总延迟和探索权重。未知额度保持 unknown，不当成零或无限。
8. 免费池与专业池使用不同路由图和账本；任何 fallback 都不能跨资源域。
9. 调度结果保存候选摘要、排除原因、最终选择和配置版本，管理员可以解释一次请求为什么走该凭据。

### 重试、熔断与提交边界

- 重试由稳定 error kind、HTTP 状态、Retry-After、请求语义、发送阶段和剩余总预算共同决定。
- 参数、鉴权、权限、额度、内容拒绝和永久 Provider 错误不重试。
- 连接建立前失败可安全换凭据；请求体可能已经被上游接收但无回执时进入 uncertain，除非上游支持并实际使用幂等键。
- 非流式聊天在未向客户端提交且错误明确临时时允许有限重试；每次 attempt 单独记录实际 usage/费用。
- 流式响应在首个客户端字节前可按 policy 重试；提交后失败只发送规范终止/断连并记录 partial stream，不能从头拼接第二个上游。
- 429 尊重 Retry-After；没有明确值时使用带抖动的有上限退避。冷却和熔断状态可解释、带到期时间，半开探测限制并发。
- 客户端取消通过 context 传播；上游不支持取消时继续跟踪实际结果和结算，不向用户伪造取消成功。

### Usage、额度与套餐

- 预留值由输入估算、最大输出和资源价格版本计算，并标记估算来源。
- Provider 返回 usage 时保存原始脱敏值并归一为 authoritative；未返回时使用本地估算并显式标记 estimated。
- Token Plan 使用可消费精确单位余额和期限；Coding Plan 使用时间窗口、模型范围、资源域、并发/RPM/TPM 和总量上限，不表达虚假“无限”。
- 人工入账创建有操作者、理由、来源和幂等键的账本事件，不直接改余额字段。
- 重试 attempt 的上游真实消耗全部进入成本事实，但用户扣减规则由逻辑请求和套餐 policy 唯一决定，不能重复扣除。
- 失败、取消、超时、partial stream 和 uncertain 分别有结算规则；无法判断时冻结 reservation 并进入人工/自动恢复队列，不猜测释放。
- 上游余额查询结果与本地账本分开保存。查询不到时展示 unknown；上游营销“免费”不等于可计算余额。

### 管理端与 Playground

主导航按任务组织，不按数据库表堆菜单：

1. 总览：服务健康、请求/Token、成功率、TTFT、错误、队列、资源池和告警。
2. Provider 与模型：Provider、模型能力、端点、资源域、映射、草稿校验、发布和回滚。
3. 上游凭据池：密钥、授权、健康、冷却、RPM/TPM/并发、固定出口、连接/生成测试和批量启停。
4. 用户与网关 Key：邀请、审核、角色、停用、模型授权、Key、额度和套餐。
5. 用量与账本：请求 usage、额度事件、预留/结算/补偿、资源域和套餐期限。
6. 请求与审计：request/attempt、路由解释、错误、管理操作和受控内容访问。
7. Playground：文本、工具调用、reasoning、非流式与流式响应。
8. 系统设置：安全、保留期、代理出口、告警、备份和配置版本。

页面采用稳定侧栏、紧凑页头、工具栏、桌面表格与移动列表。异步操作必须有提交、校验、排队、发送、等待、完成/失败等 typed 状态；错误内联展示 request ID、阶段和可行动建议，不以 toast 代替事实。审美由 owner 验收，自动测试只保护结构和交互合同。

### 安全与数据

- 管理密码使用 Argon2id；网关 Key 和会话使用高熵随机值并只保存带 server pepper 的摘要；Key 明文只显示一次。
- 上游凭据使用版本化主密钥 + AEAD 信封加密；支持新增密钥版本、后台轮换、失败恢复和旧版本清理，不把主密钥写入数据库或 Git。
- 管理端使用可撤销服务端会话、HttpOnly/Secure/SameSite Cookie、CSRF 防护、登录限流和会话审计。
- 角色至少为 administrator、operator、member；内容审计、密钥管理、额度调整和配置发布分别授权，不用一个 `is_admin` 覆盖所有敏感操作。
- 自定义 Base URL 和代理统一限制 scheme、端口、解析 IP、重定向与 DNS 重绑定；默认拒绝 loopback、link-local、私网和云元数据地址，除非部署策略显式受控允许。
- 请求体、上下文、流时长、队列长度、响应大小、并发和日志都有配置上限。
- 普通日志、trace、metric 和错误默认脱敏。请求内容进入独立加密受控存储，具有角色权限、访问审计、保留期、删除和导出规则。
- 管理配置变更使用乐观并发和审计；危险操作需要明确确认，批量操作返回逐项结果而非整体假成功。

### 关键取舍

- 采用模块化单体而非微服务：当前几十人规模不需要分布式发布成本；模块 owner、PostgreSQL 和 Valkey 已为水平扩展保留边界。
- 采用 PostgreSQL append-only 账本而非可变余额：并发预留、补偿、人工调整和历史解释需要可审计事实。
- 采用 Valkey 只做临时协调而非权威数据：限流和租约需要跨实例原子操作，但缓存丢失不能损坏额度或任务事实。
- 采用显式 adapter + canonical model 而非巨大 Provider if/else：厂商变化不应污染调度、账本和公共协议。
- 采用 OpenAI-compatible 明确子集而非“什么都透传”：静默丢字段会制造不可解释行为；支持矩阵和拒绝比假兼容可靠。
- 生产前直接重建 schema 而非积累迁移：当前无生产数据，保留错误中间形态只增加长期成本。
- 前端静态资源嵌入 Go 二进制而非独立 Node 生产服务：降低 Windows/Linux/macOS 小规模部署复杂度。
- 不 fork 参考网关：复用成熟 Go/React/PostgreSQL/Valkey 组件机制，但协议语义、调度、账本和恢复由 LLMGateway 自己拥有。

## 生产级切片

每个切片独立达到实现、错误、并发、中断、恢复、安全、可观测性、测试、文档和目标环境验证闭环。数字只表示执行顺序，切片名称表达业务结果。

当前执行状态：切片 1 进行中；后续切片尚未开始。只有完成对应实现、验证和文档闭环后才勾选。

- [ ] **切片 1：可运行、可验证、可恢复的服务底座**
  - Go module、目录依赖守卫、类型化配置、结构化日志、request ID、OpenTelemetry、Prometheus、健康/就绪、优雅停机。
  - PostgreSQL migration/sqlc、Valkey 接线、事务与租约基础、React/Vite 工作区、Go embed、统一错误壳。
  - 唯一完整验证命令、CI、生成漂移、secret/license/dependency 扫描、跨平台构建骨架。
  - 数据库初始化、备份/恢复演练和基础进程强杀恢复测试。

- [ ] **切片 2：受控用户、权限与网关 Key**
  - 一次性管理员 bootstrap、邀请注册、审核、登录/登出、会话撤销、角色权限、用户停用。
  - 网关 Key 创建、只显示一次、摘要验证、撤销、有效期、模型授权和调用审计。
  - 管理端登录、用户、邀请、Key 与权限页面；并发 claim、重复审核和停用中的请求边界。

- [ ] **切片 3：可发布的 Provider、模型与凭据池**
  - Provider/模型/能力/资源域/固定出口、凭据加密与轮换、模型授权、健康状态。
  - 草稿编辑、完整校验、不可变版本发布、数据面快照、乐观锁、回滚和审计。
  - 通用 OpenAI-compatible adapter 基础、连接测试的“无 Token 探测/可能计费生成”区分。
  - 管理端 Provider、模型、凭据、配置发布和逐项批量结果。

- [ ] **切片 4：带账本与公平 admission 的文本请求闭环**
  - `GET /v1/models` 与 `POST /v1/chat/completions` 非流/流、canonical message/tool/reasoning/usage/error。
  - 用户模型授权、免费/专业资源域、Token/Coding entitlement、原子 reservation/settlement/compensation。
  - 有界公平排队、多维并发/RPM/TPM、通用 Provider 路由、request/attempt 事实和取消传播。
  - 首个真实 OpenAI-compatible 隔离 Provider 验收；断连、partial stream、重复和崩溃恢复。

- [ ] **切片 5：GLM、DeepSeek、Agnes 与 Responses**
  - 智谱、DeepSeek、Agnes adapter，Function Calling、thinking/reasoning、工具轮次回放、流解析、usage 和错误分类。
  - Responses create/retrieve/delete/input-items/cancel、后台 response 状态与标准流事件。
  - 模型 capability matrix、支持字段合同和 unsupported 明确拒绝。
  - 三家真实隔离请求：非流、流、工具循环、reasoning、429/5xx fixture 与 request ID。

- [ ] **切片 6：多凭据韧性、粘性与可解释调度**
  - 硬资格过滤、候选评分、探索、会话粘性/逃逸、已知/估算/未知额度、多 attempt 成本事实。
  - 有界重试、Retry-After、冷却、熔断/半开、同能力故障切换、免费/专业域隔离。
  - Valkey 跨实例 token bucket、并发租约、公平 ticket 和丢失重建；数据库/Valkey 故障降级边界。
  - 管理端路由解释、健康、队列和策略配置；确定性时钟/随机源、竞态和负载测试。

- [ ] **切片 7：运营、审计、告警与发布交付**
  - 总览、用量账本、请求/attempt、错误、审计、受控内容访问、保留/删除和导出。
  - 文本、工具调用、推理与流式 Playground；真实提交步骤、进度、结果、错误和取消反馈。
  - 健康与额度告警 adapter、数据备份恢复、主密钥轮换、配置回滚和故障运行手册。
  - 单机真实链、多实例一致性、优雅升级/停机、容量上限与长流排空。
  - Windows/Linux/macOS x64/ARM 构建，Docker image、校验和、SBOM、发布文档与最终生产验收。

## 实施任务

### 全局合同与工程入口

- [ ] 创建 Go module、前端 workspace、版本锁文件和目录依赖守卫。
- [ ] 实现类型化配置 schema、启动校验、开发/测试/生产 profile 和敏感值来源。
- [ ] 建立 migration、sqlc、生成命令、生成漂移检查和首次生产前 schema 重建命令。
- [ ] 建立统一 `scripts/verify.ps1`，覆盖格式、lint/vet、单元、集成、race、前端类型/测试/构建、migration、Compose、生成漂移、依赖/许可证、secret 和文档链接。
- [ ] 建立 CI：Linux 主测试/race，Windows 关键测试，目标 OS/arch 构建矩阵和发布物校验。
- [ ] 建立本地 fixture Provider、可编程故障代理和确定性时钟/随机/ID 测试设施。

### 数据、身份与安全

- [ ] 设计并实现身份、Provider、凭据、配置、请求、attempt、账本、entitlement、审计和内容留存 schema。
- [ ] 实现管理员 bootstrap、密码哈希、会话、CSRF、邀请、审核、角色权限与登录限流。
- [ ] 实现网关 Key 前缀/摘要/pepper、创建一次展示、撤销、过期、模型授权和审计。
- [ ] 实现上游凭据 AEAD 信封加密、主密钥版本、轮换、失败恢复和内存最短解密边界。
- [ ] 实现 SSRF/重定向/DNS 重绑定防护、资源大小/时长/并发上限和日志脱敏。
- [ ] 实现内容审计的独立加密存储、权限、访问审计、保留期、删除和导出。

### 配置、Provider 与协议

- [ ] 实现 Provider/模型/capability/resource-domain/fixed-proxy registry 与来源核验时间。
- [ ] 实现配置草稿完整校验、不可变发布、单一 active revision、数据面快照、回滚与审计。
- [ ] 实现 canonical message/content/tool/reasoning/usage/error 类型和不可表达能力错误。
- [ ] 实现 Models、Chat Completions、Responses 公共协议 parser/presenter 与 OpenAPI 文档。
- [ ] 实现通用 OpenAI-compatible、Zhipu、DeepSeek、Agnes adapter 和确定性 wire fixture。
- [ ] 为每个 Provider 建立连接测试、真实生成测试、能力矩阵、错误分类和 usage 来源测试。

### Admission、调度、韧性与账本

- [ ] 实现按资源域隔离的有界公平 admission、排队超时、取消和用户/Key/模型优先级。
- [ ] 实现 Valkey 多维 token bucket、带 TTL 并发租约、原子脚本、续租、释放和丢失清理。
- [ ] 实现 credential eligibility、评分、探索、粘性、逃逸、健康、冷却与可解释路由记录。
- [ ] 实现 retry budget、Retry-After、退避、熔断、半开和流提交/uncertain 边界。
- [ ] 实现 append-only ledger、reservation、authoritative/estimated usage、settlement、release、compensation 和人工 adjustment。
- [ ] 实现 Token Plan、Coding Plan、期限、模型/资源域范围、额度、并发/RPM/TPM 与过期处理。
- [ ] 实现免费池耗尽明确错误，并以测试证明所有失败路径均不进入专业池。

### 管理端与运营

- [ ] 实现管理端设计 tokens、响应式应用壳、权限路由、typed API client 和统一异步状态组件。
- [ ] 实现总览、Provider/模型、凭据、用户/Key、账本、请求/审计、设置和语言模型 Playground 页面。
- [ ] 实现 request/attempt/ledger/audit 查询、筛选、分页、导出和受控正文访问。
- [ ] 实现 metrics、trace、structured logs、health/readiness、告警接口和运行状态页数据。

### 交付与真实验收

- [ ] 建立数据库备份、恢复、校验、配置回滚、主密钥轮换和灾难恢复脚本/文档。
- [ ] 验证客户端断连、长流排空、SIGTERM/Windows stop、进程强杀和主机重启恢复。
- [ ] 验证 PostgreSQL/Valkey 短暂故障、Valkey 全丢、配置发布竞态和多实例租约一致性。
- [ ] 使用隔离账号真实验收 GLM、DeepSeek 和 Agnes 的本轮接入合同，不打印密钥或正文。
- [ ] 使用标准 OpenAI SDK/HTTP fixture 验收模型列表、Chat Completions 和 Responses 合同。
- [ ] 构建并校验 Windows/Linux/macOS x64/ARM 二进制、Docker image、SBOM 和校验和。
- [ ] 同步 README、spec、architecture、dev、SECURITY、CONTRIBUTING、API 与运维文档。
- [ ] 运行唯一完整验证、真实核心路径、差异/敏感信息检查，并记录未验证项与剩余风险。

## 恶劣路径矩阵

| 边界 | 接受/提交事实 | 失败状态 | 恢复 owner | 重放与幂等 | 验证证据 |
| --- | --- | --- | --- | --- | --- |
| 重复同步请求 | admission 后创建逻辑 request + reservation | duplicate/in-progress/completed | Public Protocol + Ledger | Idempotency-Key 绑定请求摘要；冲突拒绝 | 并发相同键测试 |
| 邀请码重复 claim | claim 与用户创建同事务 | claimed/expired/revoked | Identity | 唯一约束，重复返回稳定结果 | 并发 claim 集成测试 |
| 重复管理员操作 | 乐观版本 + audit | conflict/already-applied | 对应 owner | 幂等键或版本条件 | 双击/并发更新测试 |
| 客户端排队时断连 | 尚未 admission，不建 reservation | canceled-before-admission | Admission | 删除 ticket，无上游副作用 | 断连与队列清理测试 |
| admission 后断连 | request/reservation 已持久化 | canceled/dispatching/uncertain | Request workflow | 按发送边界取消、结算或冻结 | 可控 barrier 测试 |
| 上游连接前失败 | attempt 未提交请求体 | provider_temporary | Resilience | 可换合格凭据，有总预算 | 故障代理测试 |
| 请求体发送后无回执 | attempt send state 不确定 | uncertain | Request recovery | 无可靠幂等键不重放 | 半关闭连接测试 |
| 上游 429 | attempt 保存 Retry-After/错误 | cooling/rate_limited | Resilience | 有界等待或换同域凭据 | 429 fixture + fake clock |
| 上游 5xx/超时 | error kind + send state | temporary/uncertain | Resilience | 仅满足安全边界时重试 | 状态矩阵测试 |
| 流首字节前失败 | 未向客户端 commit | provider_temporary | Resilience | 预算内可重试 | delayed-header fixture |
| 部分流后失败 | 已 commit + partial usage | stream_interrupted | Public Protocol + Ledger | 不重放；结算已知 usage | chunk 后断连测试 |
| reasoning/tool 回放缺失 | canonical 校验失败或上游 400 | invalid/provider_contract | Provider adapter | 不盲重试，暴露能力错误 | GLM/DeepSeek fixture |
| 并发额度竞争 | reservation 原子写入 | quota_exhausted | Ledger | 事务/唯一约束，无超扣 | 高并发 reservation 测试 |
| 免费池耗尽 | resource domain 已绑定 | free_pool_unavailable | Routing | 不进入专业池 | 全错误路径隔离测试 |
| 凭据中途停用 | 请求绑定配置/凭据快照 | 当前请求按已发送边界完成或取消 | Credential Pool | 后续请求拒绝；当前不静默换线 | barrier + disable 测试 |
| Valkey 短暂不可用 | 权威请求/账本仍在 PG | admission_unavailable | Admission/Resilience | fail closed，不绕过限流 | 服务断开测试 |
| Valkey 数据全丢 | 租约/计数可重建 | recovering | Coordination recovery | 从 PG/config 重建，旧租约 TTL 清理 | flush + 并发测试 |
| PostgreSQL 短暂不可用 | 无法持久接受/预留 | storage_unavailable | Store/Request workflow | 发送前 fail closed | DB 断开测试 |
| 进程强杀 | 已提交事实停留在明确状态 | queued/dispatching/uncertain/running | Recovery workers | 租约接管，幂等恢复 | kill/restart 集成测试 |
| 配置发布竞态 | draft revision + expected version | conflict | Configuration | 单一 active revision，重试需重读 | 并发 publish 测试 |
| 重复取消 | cancel intent 持久化 | canceled/not-cancelable | 对应 workflow | 返回当前真实状态 | 并发 cancel 测试 |
| 主密钥轮换中断 | 每条密文记录 key version | partially-rotated | Credential crypto | 逐条幂等，双 key 只在轮换窗口读取 | kill/restart 轮换测试 |
| 内容审计越权 | 独立权限与访问事件 | forbidden | Identity/Audit | 不返回正文，失败也审计 | 权限矩阵测试 |
| 恶意 URL/重定向 | 解析前策略校验 | ssrf_blocked | Security adapter | 每次重定向重新校验 | DNS/redirect fixture |
| 优雅停机超时 | stop accepting + drain deadline | interrupted/uncertain | Runtime | 未完成请求持久化后退出 | 长流 shutdown 测试 |

## 验证计划

### 定向检查

- Go：`gofmt`、`go vet`、单包单元/集成测试、sqlc/goose 生成与 migration round-trip。
- Frontend：格式、lint、TypeScript、Vitest/Testing Library、生产构建，以及 Playwright 真实浏览器核心路径与桌面/移动结构验收。
- Protocol：OpenAI wire fixtures、SSE/event parser、Function Calling、reasoning、usage、error schema。
- Provider：每个 adapter 的 request/response/stream/error/usage fixture 与 capability matrix。
- 数据：事务、状态机、唯一约束、账本不变量、配置发布、恢复 worker 和密钥轮换。

### 唯一完整验证

最终根入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1
```

它必须从干净 checkout 可重复执行，并覆盖：

- 环境与生成工具版本；
- Go format/vet/test、Linux CI race、静态分析；
- 前端 format/lint/typecheck/test/build；
- sqlc/OpenAPI/路由生成漂移；
- PostgreSQL migration up/down/rebuild 与 schema 检查；
- Compose config、集成测试、故障注入和恢复测试；
- secret、dependency、license、vulnerability、SBOM 和 Markdown 链接；
- 目标二进制和 Docker image 构建。

### 竞态与并发

- `go test -race` 在 Linux CI 覆盖调度、租约、流、账本和恢复核心包。
- 数据库并发测试覆盖邀请 claim、reservation、settlement、人工调整和配置发布。
- 可控 barrier/fake clock 替代固定 sleep，重复运行不是通过条件。
- 负载验收覆盖几十名用户、长短请求混合、慢客户端、队列饱和和单用户恶意占用。
- 多实例验收覆盖共享限流、租约接管、粘性、配置一致性和实例退出。

### 目标平台

- Windows amd64：本地开发、完整非 race 验证、服务启停和单二进制运行。
- Linux amd64：完整验证、race、Docker image、单机与多实例真实部署验收。
- Linux arm64：交叉构建并在可用 ARM 环境运行 smoke；没有真实 ARM 运行证据时不得声明已运行支持。
- macOS amd64/arm64 与 Windows arm64：至少可重复交叉构建；发布声明严格区分“已构建”与“已运行验证”。

### 隔离的真实 Provider 验收

- 凭据只从本地未跟踪环境注入；命令、日志、fixture 和错误全部脱敏。
- GLM、DeepSeek、Agnes 分别验证本轮合同：认证、非流、流、工具调用、多轮 reasoning、usage、无权限模型和临时错误。
- 真实验收使用独立命令，不进入每次日常确定性测试；未运行时明确报告，不能用 mock 冒充。

### 安全与恢复

- 密钥/密码/会话摘要、AEAD 篡改、主密钥轮换、日志脱敏和前端不可见性。
- SSRF、redirect、DNS rebinding、代理认证、请求/响应大小、压缩炸弹和长流上限。
- 数据库备份后写入新事实、恢复到隔离实例、校验账本/任务/配置一致性。
- Valkey flush、PostgreSQL/Valkey 断开、进程 kill、主机重启模拟、磁盘只读/空间不足可行动错误。
- 配置误发布回滚、凭据批量停用、免费池全部耗尽和告警链。

## 收口

### 完成事实

- 当前仅完成计划编写前的架构、环境和规则基线；本计划内所有生产级切片尚未开始实施。

### 已执行命令与结果

- `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-environment.ps1`：通过。
- `docker compose config --quiet`：通过。
- PostgreSQL/Valkey stop/start 持久化探针：通过，探针已清理。
- 基线提交 `f17ce79`：已推送 `origin/master`。

### 未验证项

- 所有产品代码、schema、API、管理端、Provider、调度、账本和生产部署均尚未实现，因此尚无产品验收结果。
- Provider 当前模型、额度、错误和完整文本 wire 必须在对应切片开始时重新核验。
- ARM/macOS 真实运行环境目前未验证。

### 剩余风险

- Provider 合同和免费额度变化快，必须依靠 capability/source/verified-at 数据与隔离真实验收，而不是静态 preset 永久正确。
- 流式提交、unknown side effect、跨实例公平和账本原子性是最高风险边界，不能拆成后补的“优化”。
- 请求内容留存满足滥用调查但显著提高敏感数据责任，权限、加密、审计、保留与删除必须同一切片闭环。

### 外部操作状态

- 架构与环境基线已 commit 并 push。
- 计划提交 `5fa5278` 已推送 `origin/master`；owner 已授权从零到一连续执行、验证、提交和推送。
