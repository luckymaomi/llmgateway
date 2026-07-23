# 控制台用户友好性闭环

## 目标

一次性把 LLMGateway 已有的多 Provider 服务与管理员/成员治理能力，投影成首次使用者可以直接运营的正式控制台。功能存在但用户无法发现、理解、完成或排障，不算完成。

正式实现以根目录 `console-concept/` 为交互合同。该目录长期保留为可打开的设计参考，但不连接真实 API、不进入生产构建，也不拥有第二套身份、额度、健康、成本、调度或账本事实。

## 产品边界

- 管理员控制台直接提供：运营总览、运维监控、Provider、模型、上游 API Key、配置发布、成员、邀请、Gateway Key、订阅与额度、API 日志、额度记录、上游成本、站点设置。
- 成员控制台直接提供：仪表盘、自己的订阅与额度、额度记录、Gateway Key、API 日志、账号操作。
- 管理员与成员共用控制台框架，导航按服务端 capability 投影；服务端继续独立执行最小权限，成员直接访问管理员 URL/API 仍必须被拒绝。
- 上游 API Key 与 Gateway Key 始终使用不会混淆的名称、创建入口和测试流程。
- 空、加载、失败、冷却、停用、待审核、待确认和完成状态都显示真实事实、就地反馈与下一步动作，不用帮助段落、术语解释、占位数据或虚假健康补流程。
- 桌面与移动复用同一任务；移动导航可打开、可关闭、不遮挡内容，表格只在自身边界滚动。
- 总览、运维、额度和成本优先使用真实聚合图表呈现趋势、构成与消耗；图表只投影服务端或当前查询返回的权威数据，空值保持空，不制造第二套统计或虚假趋势。
- 客户端或透明代理 IP 不参与账号停用、封禁或会话撤销。IP 只可用于明确的网络边界与限流；开发环境的透明代理 Fake-IP 兼容只属于出站 SSRF 配置，生产仍显式失败关闭。

当前不做明暗主题、支付充值、通用 OAuth 账号池、粘性会话、iframe、成员分组、公告、优惠码或兑换码。未来只有真实需求、唯一 owner、官方合同和完整安全边界成立后才能加入。

## 已确认基线

- PostgreSQL 已拥有 Provider、模型、上游 API Key、配置 revision、成员、邀请、Gateway Key、entitlement、请求、attempt、usage、成本、额度事件和审计事实；Valkey 只拥有短期限流与并发协调。
- Provider catalog 已拥有 Agnes、智谱 GLM、Google Gemini 与 OpenAI-compatible 的统一定义；硅基流动文本能力复用 OpenAI-compatible，不建立专用 adapter。
- 当前工作区已实现运营聚合、服务端分页、上游 API Key 健康、管理员/成员总览、Provider 接入预设、站点资料、额度记录、账号恢复、上游连接测试与 Gateway Key 连通性测试。正式控制台仍未完整投影这些能力。
- 接管时正式前端仍以宽泛入口和页面内 Tab 组织核心任务；成员本人额度入口未贯通，usage 列表不能完整呈现失败、取消、进行中和 uncertain 请求。当前工作树已经按本计划重建这些入口和合同。
- 旧独立请求实验页与兼容转发已删除；连接测试分别回到上游 API Key 和 Gateway Key 所属页面。
- `console-concept/` 已借鉴固定版本 Sub2API、New API、LiteLLM 与 ylsCode 的任务组织，并拒绝其支付、巨型设置、通用 OAuth 池、过量筛选和虚构上游余额。一次性 Chromium 检查已覆盖管理员 14 个入口、成员 5 个入口、1440×900 与 390×844、移动菜单、上游测试、请求筛选和详情，无控制台错误或页面横向溢出。
- 最近完整日志 `.build/test-logs/20260722T124617116289Z-everything.log` 中 full 与真实 Provider 档通过；900 秒容量除热点长流沿用旧 2 秒首字节门槛外均通过。报告已把热点长流门槛按共享等待合同单列为 5 秒，但尚未重跑最终 `everything`。
- 2026-07-23 owner 真实点击四个内置 Provider 的“接入”均得到 `Registry input is invalid.`。本机透明代理把四个权威域名解析到 `198.18.0.0/15` Fake-IP，预设安装错误地在只写禁用 registry 记录时执行运行时 DNS/SSRF 校验；账号状态与该地址无关，且现有身份实现没有基于 IP 自动停用或撤销会话。
- 正式总览、运维、额度与成本页已经用 Recharts 投影现有 operations、credential、entitlement 与 cost 查询；不跨页或跨币种补算数据，也不建立第二套聚合 owner。

## 接管失败证据

- 2026-07-22 接管核验时工作区干净，`master` 与 `origin/master` 同位于 `fdf07e5`；正式前端仍只有 6 个宽泛入口，并通过 `CatalogTabs`、`AccessTabs`、`LedgerTabs` 隐藏 11 个稳定任务，不符合概念稿的直达信息架构。
- 成员端路由显式禁止 `/ledger/entitlements`，服务端 `GET /api/control/entitlements` 与 `POST` 共用管理员中间件；因此成员无法读取自己的 entitlement，虽然 quota service 已能按 principal 约束查询。
- 接管时 `GET /api/control/usage` 读取 `ListRequestUsage`，响应只有已产生 usage 的时间、用户、Key、模型、Token 与 Request ID；进行中、失败、取消和 uncertain 请求没有正式控制面出口，也没有状态/成员/Key/模型/Request ID 的完整筛选与发送边界详情。当前工作树已由 `/api/control/requests` 和 request detail 取代该合同。
- 最近授权长测 `.build/test-logs/20260722T124617116289Z-everything.log` 的 full、真实 Provider 均通过；容量报告 `.build/capacity-capacity-42064-40d6f86c/capacity-report.json` 记录 80,923/80,923 个稳态请求完成、故障矩阵和资源回收通过，唯一失败是热点 32 路长流首字节 p95 3.306 秒仍被旧 2 秒门槛拒绝。该场景共享有界等待的目标门槛为 5 秒，必须核验脚本与报告 owner 后修正并从统一入口重跑。

## 实施顺序

1. 以 capability 和角色重建唯一正式路由/导航，建立管理员 14 个、成员 6 个直达任务，删除三个旧 Tab 组件和旧路由合同。
2. 在 quota/request owner 上补齐成员本人 entitlement GET 与全状态请求日志查询/详情，保持写入、其他成员事实和采购成本仅管理员可用。
3. 将现有页面和 operations 聚合投影到新任务入口，补齐空、加载、失败、冷却、停用、待审核、待确认、完成状态以及桌面/移动操作闭环。
4. 收敛只绑定旧信息架构的测试，运行定向检查和真实有头 Chromium 管理员/成员主旅程；长档按 owner 后续决定留给发布候选验收。
5. 修复内置 Provider 安装与透明代理 Fake-IP 的职责漂移；预设安装只持久化 catalog 拥有的禁用记录，真实探测和请求继续执行 SSRF 策略，开发入口显式兼容本机透明代理而不改变生产默认。
6. 按概念稿把现有 operations、quota 与 costing 查询投影成真实图表；图表不得跨页或跨币种伪聚合，文字与表格只保留精确事实和操作出口。

## 实施原则

- 当现有模块职责、状态模型、公共合同或信息架构已不适合本计划，且重建能更直接地恢复唯一事实 owner 时，直接重建该模块并在同一切片一次性替换所有消费者；不在错误模型上叠补丁，不保留旧入口、兼容转发、别名、双读双写或准备随后删除的桥接层。
- 重建不降低生产门槛。新模块仍需同时闭合权限、持久化、并发、中断、失败、恢复、观测、测试、文档以及桌面和移动真实用户路径。

## 唯一事实投影

- Provider 接入预设只来自现有 provider catalog；安装事务只创建 registry 中的 Provider 与模型，不接收或保存 secret。
- 上游 API Key 健康由持久 attempt 派生成功率、延迟、最近错误与冷却；页面不重算、不伪造上游余额。
- 总览由 operations 查询组合权威事实，不持久化第二份统计。多币种成本分开显示，成员永远看不到采购成本。
- entitlement、额度记录和请求日志在 SQL 层按 principal、筛选、稳定排序和分页；成员查询强制绑定本人，不能接受任意 owner ID。
- API 日志覆盖所有已接受请求；Token 未知保持未知，错误只暴露稳定类别与 Request ID，不返回正文或上游 secret。
- 上游 API Key 测试必须选择已绑定模型并走真实 Provider adapter 的最小生成；Gateway Key 测试必须走统一 request workflow。两者都不得建立旁路或盲目重放未知副作用。
- 站点资料由 PostgreSQL 类型化 owner、版本 CAS 和审计维护；部署 secret、数据库、Valkey、TLS、备份与签名身份不进入网页设置。
- 内置 Provider 预设由 catalog 在进程启动时校验静态合同；安装事务不解析 DNS、不发上游请求，只创建默认停用的 Provider 与模型。启用前的凭据测试和正式发送继续通过 SSRF-safe transport 校验每次解析与重定向。
- 身份 owner 不根据客户端 IP 或出站代理解析结果改变用户、会话或审核状态；登录来源仅在受信代理边界下服务明确限流，不能演变成账号事实。

## 实施清单

### 已完成的准备

- [x] 完成参考项目固定版本、许可证、截图、成熟机制与拒绝项研究。
- [x] 核验 Provider、identity、quota、ledger、operations、配置发布和前端消费者。
- [x] 删除不存在的人工调整合同、旧请求实验页及绑定临时 UI 的重复测试。
- [x] 建立 Provider 预设、站点资料、运营聚合、数据库分页、上游测试和 Gateway Key 测试的后端与当前页面入口。
- [x] 完成并验证独立管理员/成员控制台概念，明确长期保留但不接生产构建。

### 正式控制台落地

- [x] 按 capability 重建正式管理员/成员主导航，把稳定任务从隐藏 Tab 提升为直达入口；删除正式前端旧导航、旧 Tab、旧命名和兼容路径。
- [x] 把管理员总览、运维、Provider 预设、上游 API Key 健康与就地测试、配置发布和站点资料接到现有唯一 API。
- [x] 把成员、邀请、Gateway Key、订阅与额度、额度记录和上游成本按角色投影；开放成员本人 entitlement 读取，POST 与任意成员读取仍仅管理员可用。
- [x] 把 usage 页面重建为全状态 API 日志，支持有界时间、成员/Key、模型、状态与 Request ID 查询，并可进入稳定错误和发送边界详情。
- [x] 让管理员和成员从空系统、正常运行、失败排障三种状态都能自然完成下一步；同步桌面与移动布局、焦点、加载、错误和空状态。
- [x] 正式页面完成后复核 `console-concept/` 仍可独立打开且未进入生产构建，不把模拟数据复制到正式实现。
- [x] 修复四个内置 Provider 预设安装的 DNS/SSRF 职责漂移，并在透明代理环境从真实入口确认安装形成停用 Provider 与模型；不增加按钮、路由或文案级长期测试。
- [x] 用现有真实 overview、credential、entitlement 与 cost 查询补齐总览、运维、额度和成本图表，覆盖真实空/加载/失败状态及移动布局，不新增第二套聚合 owner。

### 测试与收口

- [x] 删除因本次导航、标题、按钮、文案、颜色、顺序、组件或文件变化而失败的脆弱测试；只保留稳定业务结果及权限、账本、幂等、并发、发送边界、安全与恢复不变量。
- [x] 运行一分钟内定向检查，修复真实合同、类型、格式、构建和生成漂移：`go tool sqlc generate`、quota/control/store 定向测试与 `pnpm.cmd run verify` 均通过。
- [x] 用真实 Go、PostgreSQL、Valkey、生产前端和有头 Chromium 复用同一管理员/成员主旅程，覆盖桌面/移动、刷新、重登、越权、连接测试、额度、日志详情与发送边界脱敏、失败排障和持久状态；`scripts/test-browser-real.ps1` 于 2026-07-23 完整退出 0。
- [x] owner 后续明确改为自行运行长档；本次 Agent 不再运行 `everything`，只运行一分钟内定向检查和一次性真实浏览器验收，最终如实记录未验证项。
- [x] 同步 `spec.md`、README、`history.md`、运行事实与本计划。
- [x] 检查 secret、差异和生成物后单次提交并推送，工作区保持干净。

## 完成标准

- 全新管理员不需要理解 slug、kind、Base URL、账本状态机或内部模块，就能从已验证 Provider 开始，完成上游 API Key、模型价格、配置发布、成员、额度、Gateway Key 和首条真实请求。
- 日常管理员能直接判断哪些上游资源可用、哪些在冷却、成员还剩多少、请求为何失败以及成本来自哪里。
- 成员只看见并操作自己的额度、Key 与请求，使用页面或直接调用 API 都不能越权。
- 正式页面只投影唯一后端事实，刷新、重登、进程重启和多实例后结果一致；没有 404、占位成功、双轨实现、兼容转发或虚假健康。
- 自动测试不冻结高频变化的界面；真实管理员/成员主旅程通过、定向检查通过且所有已知正式入口故障修复后标记本计划完成。`everything` 是发布候选组合证据，由 owner 自行运行，不是本次 Agent 收口条件。

## 当前收口证据

- 2026-07-23 `scripts/test-browser-real.ps1` 使用隔离 PostgreSQL、Valkey、真实 Go、生产前端和有头 Chromium 完整退出 0；主旅程覆盖管理员/成员桌面与移动、配置发布、统一 API、持久状态、刷新、重登、越权和安全后置检查。
- 同一轮一次性验收从正式 Provider 页面安装 Agnes、Google Gemini、硅基流动与智谱 GLM，四个请求均返回 201，Provider 均持久为 `disabled` 且各自模型存在；临时 Provider 名称、按钮和 SVG 断言已在通过后删除。
- 同一轮确认总览、运维、额度和成本主图在桌面与 390×844 视口真实绘制且页面不横向溢出；图表只消费现有真实查询。
- 浏览器取消时序由 requestflow 的可控 owner 测试保护；浏览器主旅程只保留 Gateway Key 统一 API 完成、SSE 内容/usage/request ID 与持久结算结果，不再赌点击时机制造 `uncertain`。
- `everything` 未在本工作树运行；它是 owner 自行运行的发布候选组合证据，不是本次控制台收口条件。
