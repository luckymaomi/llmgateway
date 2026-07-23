# 封闭式商业多模型订阅平台断裂式重建

## 需求文档

- 用户与场景：单实例服务约 200～300 名受控成员；管理员在线下完成交易后，在线创建成员、发布套餐、分配订阅、维护合法上游 API Key，成员使用自己的 API 密钥调用统一模型 API。
- 要解决的问题：当前产品仍以邀请码、资源域、逐成员 entitlement、Provider 安装和捕获/校验/发布配置为主流程；成员和上游 API Key 缺少完整 CRUD 与批量导入；控制台信息架构、表单对齐和操作反馈不符合实际任务。
- 当前阶段范围：断裂式重建产品规范、唯一数据库基线、商业领域、控制 API、数据面资格读取、正式桌面控制台、主旅程和事实文档；保留并适配公共协议、Provider adapter、admission、路由算法、冷却/熔断、安全重试、流式状态机、账本原子性、安全、观测和部署地基。
- 可验收完成标准：全新管理员可以从上下文式新手指引依次维护资源池和上游 API Key、发布套餐、创建成员并分配订阅、创建成员 API 密钥，随后通过统一 API 完成受额度保护的真实请求；所有对象具备当前产品需要的增删改查和明确错误出口，成员越权失败关闭。
- 控制台体验边界：参考 Sub2API 已验证的桌面任务组织、弹窗密度、圆角、表单间距、套餐展示与状态反馈机制，但不复制 LGPL-3.0 源码，也不引入公共注册、消费者 OAuth/Session 账号池或在线支付；选择框、字段说明和后续内容必须保持明确的垂直节奏，不能拥挤、重叠或把页面主体推出桌面视口。

## 当前事实

- 当前工作区位于 `master`，基于 `620499a`，保留本轮断裂式重建的大范围未提交改动；未执行 reset、commit、push、部署或发布。
- 唯一数据库基线只表达 `resource_pools`、`service_plans`、不可变套餐版本、`subscriptions` 和引用订阅/资源池的请求与账本；sqlc 输出稳定，migration up/down/rebuild 与统一 `full` 均已通过。
- identity 已具备管理员直接创建、编辑、停用和删除成员；registry 已具备只读 Provider catalog、资源池 CRUD、上游 API Key 单个/批量导入、secret 更换、状态、退役和探测；subscription 已具备不可变套餐版本与订阅分配/调整，真实桌面主旅程和核心集成均已通过。
- quota、requestflow、routing、coordination、costing 与 operations 已统一消费 subscription/resource pool；旧 resource domain、configuration revision、entitlement、invitation 和 Provider 安装消费者已从当前运行时代码、schema、脚本与控制台删除。
- 控制 API 与控制台只表达成员、资源池、上游 API Key、套餐、订阅、成员 API 密钥、请求、账本、成本和设置；前端 format/lint/typecheck/test/build 与生产嵌入构建均通过。
- Provider 不再拥有独立控制台页面或导航；内部 catalog 及 `GET /providers`、`GET /models` 只作为资源池校验和表单数据源。
- 2026-07-23 接力复核确认：前端 format/lint/typecheck/build、`go test ./...`、新核心脚本和真实有头 Chromium 主旅程已有通过证据；`scripts/test-provider-real.ps1` 已改为消费 code-owned catalog，并通过资源池、上游 API Key、模型价格、不可变套餐版本和管理员订阅建立现场资格链，PowerShell AST、相关 Go owner 包与差异检查通过，外部 Provider 档未运行。
- 2026-07-23 CSS 清理后重新运行 `pnpm.cmd --dir web run format`、`lint`、`typecheck` 和 `build`，四项均通过；生产构建输出当前资源池、上游 API Key、套餐、订阅、成员、API 密钥、运营与新手指引页面，不再包含独立 Provider 页面 chunk。
- 2026-07-23 CSS 清理后的 `scripts/test-browser-real.ps1` 再次实际通过（21.9 秒，其中 Playwright 6.4 秒）；1280x720 有头 Chromium 证据确认表单双列间距、40px 选择控件、模型/资源池绑定行、弹窗自身滚动、遮罩聚焦与页面主体宽度均未重叠或横向溢出。脚本后的 SQL 同时复核资源池、凭据 secret 更换/探测、不可变套餐版本、成员订阅、双角色 API 密钥、6 Token 结算和退出会话。
- 2026-07-23 `scripts/test-observability.ps1` 实际通过（86.5 秒）：固定版本 Prometheus `promtool` 识别 6 条规则，Grafana 13.1.1 成功导入并读回 operations dashboard，当前 `resource_pool` 标签合同有效。
- 初次 `pnpm.cmd run typecheck` 暴露的 API request body 泛型、缺失套餐/订阅页面和旧消费者问题已经修复；当前前端完整 verify 与统一 `full` 重复通过。
- 现有 Public API、Canonical Model、Provider adapters、路由、冷却/熔断、有界安全重试、PostgreSQL 原子 quota/usage 账本、Valkey 协调、execution fencing、崩溃恢复、secret 加密、SSRF、观测、部署和备份已有生产级实现与测试，应保留并适配。
- 参考 Sub2API `f94c693`（LGPL-3.0）与 New API `eb303d6`（AGPL-3.0）只研究机制，不复制源码。可采用机制是套餐定义与成员订阅分离、成员行直接分配、批量导入逐条反馈；拒绝金额浮点数、套餐硬删除破坏历史语义、消费者 OAuth/Session 账号池、跨组回退和测试绑定页面结构。
- owner 提供的 Agnes 失败证据：有效 Key 探测在约 8 ms 返回 `provider_temporary`，Request ID `e4d47998ac59ff6737a67ff849b44dc1`；本地现有日志没有该 Request ID，不能判定 Key 无效，重建后探测必须保留稳定类别、可行动信息和 Request ID。
- 未知项：真实 Provider 现场行为只能在不读取或打印 secret 的统一 provider 档中验证；视觉审美由 owner 验收，Agent 只验证结构、尺寸、不溢出、状态和交互。

## 失败证据

- 2026-07-23 定向运行 `go test ./internal/quota ./internal/requestflow ./internal/routing ./internal/coordination ./internal/store`：旧测试仍引用 `Entitlement` / `ResourceDomain`，旧成本与 operations repository 仍读取已删除字段，证明后端处于不可编译的断裂中间态。
- 2026-07-23 运行 `pnpm.cmd run typecheck`：首批错误覆盖 API request body 泛型、缺失套餐/订阅页面和仍参与编译的邀请、资源域、Provider/模型编辑、配置 revision、entitlement 与旧 onboarding tour，证明前端尚未完成断裂式删除。

- 管理员第二次编辑上游 API Key 时无法可靠替换 secret，废弃 Key 无删除/退役入口，多个 Key 只能逐个添加。
- 有效 Agnes Key 的连接测试被压缩为不可行动的 `provider_temporary`，用户只能看到“可以重试”，无法区分本地网络策略、上游状态、鉴权或 wire 合同。
- 管理员必须理解“资源域、捕获、校验、发布 revision”才能让已配置 Key 生效；这与“配置合法资源后立即可用”的产品合同冲突。
- 管理员不能直接创建成员，只能先发邀请码；套餐不存在，额度以零散 entitlement 直接分配，无法表达可复用产品与不可变历史版本。
- Provider、成员、凭据和额度页面绑定旧对象，表格/表单/操作列缺少统一稳定高度与居中容器，常见动作不完整。
- 真实桌面验收还需重点检查 CSS 密度：选择框、字段说明、状态和后续内容不能挤成一团；圆角保持克制，主内容区应充分利用桌面宽度，表单和表格必须有明确间距层级。
- 2026-07-23 owner 补充验收：新手指引必须使用遮罩和聚焦框锚定当前真实操作位置；现有页面虽按持久事实给出下一任务，但驱动组件缺失，尚未达到该交互合同。
- 2026-07-23 新核心集成旅程首先暴露 6 个测试夹具仍写入已删除的 `users.approved_at`，以及 execution fixture 清理未先删除复合外键 request attempt；现已按唯一基线修正，未引入兼容字段。
- 真实探测进一步证明后端执行对象未返回 `request_id` 且可能序列化 `response_text`；现已由 registry service 注入当前 Request ID，并明确禁止正文进入控制 API JSON。
- 2026-07-23 接力静态检查确认真实 Provider 档曾调用已删除的 `/providers` 写接口、模型写接口、`/configuration/revisions` 和 `/entitlements`；已断裂式删除这些调用并完成当前 schema/API 的逐段复核，不保留旧测试专用兼容端点。
- 2026-07-23 首次运行 operations 隔离验收在离线管理员恢复处返回 PostgreSQL `column "approved_at" does not exist`；根因是 `internal/store/administrator_recovery.go` 的权威更新 SQL 仍写已从唯一基线删除的字段，已直接移除该写入，未恢复兼容列。
- 2026-07-23 `full` 前静态复核发现 `scripts/test-core.ps1` 仍用已删除的 `TestPersistentQuotaLifecycleAndConcurrentReservations` 名称过滤 quota 集成测试；Go 的零匹配会返回成功，使旧 core 证据漏跑并发订阅额度用例。脚本已改为当前 `TestPersistentSubscriptionQuotaIsIdempotentAndAtomic`，并对所有 `go test -run` 引用与现有函数完成逐项匹配。
- 修正测试过滤器后重新运行 `scripts/test-core.ps1` 实际通过（20.2 秒），输出明确显示订阅额度、request execution fencing 与 Valkey 三组定向测试均真实执行，而非零匹配绿灯。
- 2026-07-23 首次统一 `full` 在 migration rebuild 首个失败：`00001_baseline.sql` 的 Down 先删除 `models`，但 `gateway_key_models` 仍保留外键引用。已按当前外键图把 API 密钥模型绑定和 API 密钥逆序删除移到模型之前，不使用 `CASCADE` 掩盖依赖遗漏。
- 修正后单独运行 `scripts/test-migrations.ps1` 实际通过（7.2 秒），唯一基线 up、down、rebuild 的逆序依赖完整成立。

## 最终目标

- 产品只有一个单实例、两个角色和五类核心业务对象：成员、套餐及版本、成员订阅、成员 API 密钥、上游资源池及上游 API Key。
- Provider catalog 是代码拥有的内部能力目录；管理员不安装或单独浏览 Provider，只在创建资源池时选择平台能力。合法资源写入 PostgreSQL 后在同一事务边界生效，不存在人工配置发布状态机。
- 套餐版本一经发布不可变；编辑产生新版本，既有订阅继续引用原版本。订阅拥有额度、周期、模型/资源池资格和速率限制，账本只引用订阅。
- 请求资格链固定为 `API 密钥 -> 活动成员 -> 活动订阅 -> 套餐版本 -> 模型/资源池 -> 合格上游 API Key -> Provider adapter -> usage 结算`。
- 上游 API Key 支持单个与按行批量导入、secret 更换、探测、启用、停用、退役；完整 secret 不回显，退役后不再调度，历史 attempt 保留脱敏引用。
- 管理员直接创建、编辑、停用和删除成员；删除是保留审计/账本引用的终态，不允许登录或调用。初始密码和重置密码只显示一次。
- 控制台只支持桌面，按任务组织并保留上下文式遮罩新手指引；引导根据真实持久状态聚焦当前操作位置，页面不拥有第二套权限、额度、健康或完成度事实。

## 不做范围

- 公共注册、邀请码、组织/租户/企业隔离、在线支付、充值、开票、自动售卖、消费者 OAuth/Session 账号池、批量注册、接码、养号、跨上游条款绕过。
- Kubernetes、多地域主动高可用、移动端控制台、图像/视频/语音/Embedding/Rerank 协议。
- 为旧 schema、旧 API、旧路由、旧页面、本机开发数据或旧测试建立 migration、别名、转发、双读双写和兼容壳。
- 纯 CSS、颜色、文案、按钮位置和 DOM 结构的长期测试。

## 设计

### 事实 Owner

- Provider Catalog：`internal/providers` 拥有支持的 Provider、固定端点、能力和现场验证元数据；控制台只读投影。
- Resource Pool / Credential：PostgreSQL 资源池、模型绑定和上游 API Key 拥有实时调度资格；`registry` 强制 provider 匹配、状态、priority、weight、限额、冷却、探测和退役。
- Plan / Subscription：新 `subscription` 领域拥有套餐、不可变发布版本、模型/资源池范围、成员订阅状态和周期；quota 账本只消费活动订阅容量。
- Identity and Access：`identity` 拥有管理员直接开户、成员状态、会话和成员 API 密钥；服务端 capability 独立强制。
- Request Workflow / Ledger：现有 requestflow、quota、execution、routing、coordination 继续拥有接受、预留、发送、恢复和结算；改为消费 live registry 与 subscription 资格，不消费配置 revision。
- Operations / Onboarding：operations 只组合各 owner 的计数和健康；新手指引只根据持久事实推导下一任务，不保存进度。

### 数据与控制流

- 基线删除邀请、资源域和配置 revision 表；新增 `resource_pools`、`service_plans`、`service_plan_versions`、版本模型/资源池连接与 `subscriptions`，并把 credential、request、ledger 外键切换到新 owner。
- 内置 Provider 和已核验模型由确定性 seed 与代码 catalog 对齐；管理员维护资源池及凭据，不创建 adapter 类型。
- 成员创建、套餐发布、订阅分配、Key 创建和凭据批量导入均使用幂等 mutation 与事务；提交未知时按 mutation 结果协调，不盲目重复副作用。
- 请求解析模型时同时验证 API 密钥范围和活动订阅版本；接受事务锁定合格订阅、冻结价格与资源池资格，随后只在该资源池内选择候选。
- 订阅到期、停用成员、撤销 Key、停用/退役凭据只阻止新请求；已接受请求按持久 claim、发送事实和账本状态完成或恢复。

### 安全与数据

- 上游 secret 使用现有版本化 AEAD，批量导入响应和审计不包含 secret；成员 API 密钥、会话和 CSRF 只保存 pepper 摘要。
- 成员删除保留历史外键与审计，清除活动会话并禁止新调用；唯一管理员不能由在线成员删除流程移除。
- 探测使用 SSRF-safe transport，错误返回稳定 kind、可行动摘要、retryable 和 Request ID；不返回上游正文、secret 或敏感 header。
- 页面和日志不持久化一次性密码、完整 API 密钥、上游 secret 或请求/响应正文。

### 关键取舍

- 采用“保留稳定数据面、重建产品层”，因为公共协议、调度、账本、恢复和安全已经是独立可靠 owner；全仓清空会重写高风险正确性而不增加产品价值。
- 采用 live registry 原子生效，删除人工 revision 发布；当前资源对象的每次写入都是单一事务，不存在必须跨多个草稿对象统一发布的真实需求。
- 采用套餐不可变版本而非直接修改 entitlement，保证既有订阅的额度和模型合同不会被后台编辑悄悄改写。
- 采用资源池作为明确资格边界，拒绝匿名跨池 fallback；同一订阅版本可以显式授权多个池，但一次请求冻结选定池，耗尽时不暗中消费其他池。
- 采用软终态删除成员和上游凭据，保留账本、attempt 和审计引用；不以物理级联删除破坏运营事实。

## 生产级切片

- [x] 规范与唯一基线：最高规约、事实文档、Skill、计划、schema 和生成合同只表达新产品，不留旧表/状态/API。
- [x] 上游资源闭环：Provider catalog 仅作为资源池表单数据源，资源池与上游 API Key 单个/批量 CRUD、探测、冷却、退役和 live routing 完整成立。
- [x] 成员订阅闭环：管理员直接开户与成员 CRUD、套餐不可变版本、订阅分配/停用/到期、成员 API 密钥和额度账本完整成立。
- [x] 请求与恢复闭环：公共 API 按订阅和资源池资格接受，限流、候选选择、usage、成本、取消、uncertain、强杀恢复与隔离的 owner 定向测试继续成立。
- [x] 桌面控制台闭环：新信息架构、上下文式遮罩新手指引、统一表单/表格对齐、空/错/加载/完成状态和管理员/成员主旅程成立。
- [x] 交付闭环：删除旧测试和文档，运行定向检查与统一 full，检查敏感信息、生成漂移和工作区边界。

## 实施任务

- [x] 完整读取规约、事实文档、README、三个 Skill、旧计划并调查工作区、参考许可证和核心链路。
- [x] 固化当前失败、采用/拒绝机制、唯一 owner、断裂边界和验证策略。
- [x] 同步 AGENTS、spec、dev、项目 Skill 与计划中的当前产品合同，删除独立 Provider 页面与导航的旧事实。
- [x] 重建唯一数据库基线、核心 SQL 查询和 sqlc 生成物。
- [x] 重建 identity、subscription、registry、quota、requestflow、operations 和 control API 消费者。
- [x] 删除服务端 invitation、resource domain、configuration revision 与 entitlement 领域及全部消费者。
- [x] 重建正式桌面控制台和按真实持久状态定位的遮罩式新手指引。
- [x] 删除绑定旧产品或纯页面形状的测试，保留并迁移只保护权限、套餐版本、批量导入、额度、路由和恢复结果的证据；`full` 前已逐项静态核对测试过滤器与当前函数名。
- [x] 同步 README、部署/运行手册、RELEASE 与 history 的当前事实。
- [x] 运行一分钟内定向检查，再运行 owner 已明确授权的统一 `full`；从首个真实根因继续修复。
- [x] 检查差异、生成漂移、敏感信息、许可证和残留旧语义，记录未验证外部事实。

## 恶劣路径矩阵

| 边界 | 接受/提交事实 | 失败状态 | 恢复 owner | 重放与幂等 | 验证证据 |
| --- | --- | --- | --- | --- | --- |
| 重复成员/套餐/订阅/Key 操作 | mutation 与业务写入同事务 | conflict 或原结果 | 对应领域 repository | 同 key 同 fingerprint 返回原结果，异 fingerprint 拒绝 | 定向 repository/control 测试 |
| 批量上游 Key 导入 | 每项状态显式返回，secret 不回显 | 单项 rejected，不伪造全成 | registry | 批次 mutation 可协调；重复项跳过 | registry 集成与真实页面 |
| 客户端断连/取消 | 已接受请求和预留已持久化 | canceled/uncertain | requestflow/quota | 取消、释放、恢复幂等 | 现有核心/浏览器旅程 |
| 上游超时、429、5xx | attempt 记录发送边界 | failed/cooling/uncertain | resilience/registry | 仅安全边界有界重试 | Provider wire 与 requestflow 测试 |
| 部分流/未知副作用 | streaming/sending 已持久化 | uncertain，不拼接响应 | execution/requestflow | 不自动重放 | 流式与强杀恢复测试 |
| 并发额度竞争 | subscription 行和 ledger 原子预留 | quota_exhausted | quota | 事务重试且不超扣 | PostgreSQL 并发测试 |
| 成员/订阅/凭据中途停用 | 新请求资格实时读取；旧请求已冻结 | 后续拒绝，旧请求按发送事实收口 | identity/subscription/registry/requestflow | 状态操作幂等 | 定向集成测试 |
| PostgreSQL/Valkey 故障 | 无法取得持久事实或共享租约 | fail closed | store/coordination | 不绕过容量，恢复从 PostgreSQL 重建 | core/full |

## 验证计划

- 定向检查：`gofmt`、`go test` 受影响 package、`go tool sqlc generate` 漂移、前端 format/lint/typecheck/build；单项预计一分钟以内。
- 完整验证：owner 已在当前任务明确授权运行 `python .\start_test.py full`，允许长时间等待并从首个真实失败继续修复。
- 竞态/并发验证：保留 quota 原子预留、idempotency、credential 状态竞争、execution fencing 和强杀恢复的最强层证据。
- 目标平台：full 中的生产前端、Go 构建、Windows 服务、TLS 双实例、备份恢复；不建立移动场景。
- 隔离的真实 Provider 验收：只有 provider/adapter 或探测 wire 改变且本机存在 owner 管理的凭据时运行统一 provider 档；不读取或打印 `key.txt`。
- 安全与敏感信息检查：secret 扫描、响应/日志脱敏、SSRF、权限越权、依赖许可证、`git diff --check`。

## 收口

- 完成事实：断裂式重建、桌面控制台、测试迁移、事实文档和统一本机验收已收口；Provider catalog 不存在独立页面或导航，当前资格链从 API 密钥到订阅、套餐版本、资源池、上游 API Key 与 usage 结算完整成立。
- 实际命令与结果：前端 `format/lint/typecheck/test/build`、`go vet ./...`、`go test ./...`、sqlc 前后哈希、PowerShell AST、`git diff --check`、真实有头 Chromium、core、operations、observability 和 migration round-trip 均通过。首次 `full` 从 migration Down 外键顺序首个失败继续，修复并定向复测后，`python .\start_test.py full` 于 2026-07-23 实际通过，用时 `0:07:47.972830`，日志 `.build\test-logs\20260723T115542076364Z-full.log`；覆盖 Windows SCM、生产 TLS 双 Gateway 滚动恢复、恢复到新数据库、加密空环境灾备、Restic 损坏检测和 Windows/Linux amd64 构建矩阵。full 后无隔离测试容器或 SCM 测试服务残留；交接时 `python .\start_dev.py --no-browser` 已启动，控制台 `http://127.0.0.1:5173` 与 Gateway `http://127.0.0.1:8080` 均实际就绪。
- 未验证项：本轮未运行会使用现场凭据的 `provider` 档，也未读取 `key.txt`；真实 Agnes 旧 `provider_temporary` 缺少当时服务端日志，不能归因于 Key。正式域名、DNS、证书、外部监控、异地备份仓库和生产主机不在本机 full 证据内。
- 剩余风险：Provider 模型、价格、额度和网络是易变外部事实，上线前仍需按正式变更窗口运行真实 Provider 与目标环境验收；视觉审美最终由 owner 判断，本轮只证明桌面结构、尺寸、不溢出、状态和交互。
- commit/push/部署状态：owner 已明确授权本次 commit 与 push；不执行部署或发布。
