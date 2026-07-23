# LLMGateway 开发历史

更新时间：2026-07-23

当前代码锚点：`b859af3` 与 2026-07-23 当前工作树

用途：给后续窗口理解 LLMGateway 为什么形成今天的产品边界、事实 owner、恢复规则、控制台结构和验收门槛。它不是 `spec.md`，不是 README，也不是当前任务计划；当前产品事实仍以 `spec.md` 为准，当前实现合同仍以根目录 `plan.md` 为准。

这份历史记录做过什么、删过什么、失败在哪里、哪些机制最后进入主干。历史里出现过的页面、接口或方案不因此恢复为当前能力。

写法规则：

- 按阶段记录，允许相邻阶段反复推翻同一问题。
- 区分聊天要求、计划、代码提交、运行证据和当前工作树；聊天里的“完成”不自动成为事实。
- 每个阶段尽量写清当时要解决什么、实际做了什么、哪条路错了、为什么删除、最后留下什么。
- Playground、旧 Tab、旧路由和兼容转发只作为历史证据，不重新进入当前产品。
- Provider secret、Gateway Key、令牌、请求正文、私有配置和个人数据不进入历史文档。
- 后续只有项目所有者明确要求时才追加或重写本文件。

证据来源：

- 当前仓库 `AGENTS.md`、`spec.md`、`dev.md`、README、`plan.md`
- 当前仓库 Git 历史：2026-07-19 至 2026-07-22 的 21 个可见提交
- Codex 全局会话目录：`C:\Users\Administrator\.codex\sessions` 与 `archived_sessions`
- 精确按 session metadata 的 cwd 核验：67 份 LLMGateway 会话，其中 23 份 VS Code 根会话、44 份派生调查或实现会话
- `.build/test-logs/`、容量报告和有头 Chromium 验收证据

会话检索说明：全文搜索曾额外命中 2026-04 和 2026-05 的其他项目会话，因为正文后来提到 LLMGateway；这些记录的 session metadata cwd 不属于本仓库，未被写入 LLMGateway 历史。当前可证明的项目演进从 2026-07-19 的 BirdAPI 初始化开始。

## 快速索引

| 阶段 | 时间 | 主题 | 最后留下的东西 |
| --- | --- | --- | --- |
| 00 | 2026-07-19 17:53 | BirdAPI 初始化 | 企业网关问题域、文档和开发约束骨架 |
| 01 | 2026-07-19 17:55 到 18:15 | 架构收束并改名 | LLMGateway 名称、`spec.md` 当前事实主干 |
| 02 | 2026-07-19 19:45 到 20:54 | 生产纪律与单文件计划 | `dev.md`、事实 owner、生命周期审查、`plan.md` |
| 03 | 2026-07-19 23:04 | 生产网关基础一次落地 | Go、PostgreSQL、Valkey、公共协议、Provider、调度、额度、控制 API、React 控制台 |
| 04 | 2026-07-20 00:25 到 11:27 | 真实浏览器与恢复 | 有头 Chromium、Provider mutation、会话/邀请隔离、幂等恢复 |
| 05 | 2026-07-20 到 2026-07-21 | 从“代码存在”转向真实用户闭环 | Provider、模型、凭据、发布、成员、额度、Gateway Key 和统一 API 主旅程 |
| 06 | 2026-07-21 21:17 | 核心业务闭环 | 多 Provider wire、凭据模型绑定、账本、恢复和真实 Provider 验收 |
| 07 | 2026-07-22 07:58 | 生产交付硬化 | 发布物、部署、备份恢复、容量、观测、供应链和 Windows 服务 |
| 08 | 2026-07-22 22:24 | 运营基线与 Playground 删除 | Provider 预设、运营聚合、站点资料、连接测试、账号恢复、唯一请求入口 |
| 09 | 2026-07-22 | `console-concept/` 信息架构合同 | 管理员 14 个任务、成员 5 个导航任务、桌面/移动概念稿 |
| 10 | 2026-07-22 到 2026-07-23 | 正式控制台投影 | capability 导航、成员本人额度、全状态 API 日志、运维页、站点资料和真实浏览器闭环 |
| 11 | 2026-07-23 | 历史文档 | 根目录 `history.md` 接住 Git、Codex 会话、失败与删减证据 |
| 12 | 2026-07-23 | Provider 接入、真实图表与测试收敛 | 修复模板安装职责漂移，补真实图表，删除固定数量和浏览器时序断言 |

## 阶段 00：BirdAPI 初始化，先定义问题而不是先堆功能

时间：2026-07-19 17:53

关键提交：`6a3ad21 chore: initialize BirdAPI repository`

最初仓库名是 BirdAPI。初始化提交主要建立文档、Skill、贡献规则、许可证和架构草案，没有假装产品已经实现。

当时要解决的问题不是“做一个转发 HTTP 的小服务”，而是一个受控企业网关如何同时处理：

- 多 Provider 和多凭据的合法利用；
- OpenAI 风格公共协议与厂商 wire 差异；
- 用户、邀请、模型权限、额度和 Gateway Key；
- 并发、限流、冷却、熔断、重试和恢复；
- secret、审计、部署和测试边界。

这一阶段做对的地方是先写边界。后来大量实现可以重建，但两个业务核心没有变化：统一 Provider 服务，以及管理员/成员治理。

最早留下的教训：

```txt
HTTP 转发不是网关事实。
身份、额度、调度、发送边界和恢复一起成立，才是可运营网关。
```

## 阶段 01：架构文档收回事实主干，BirdAPI 改名 LLMGateway

时间：2026-07-19 17:55 到 18:15

关键提交：

- `e7f4f79 docs: consolidate gateway architecture`
- `39ff631 chore: rename project to LLMGateway`

初始化后很快发现，独立的架构说明容易与产品事实重复。`docs/architecture.md` 被删除，关键边界并回 `spec.md`；随后仓库、Skill 和文档统一改名为 LLMGateway。

这次收束确立了一个长期规则：

- `spec.md` 维护当前产品和系统事实；
- 开发规约维护如何工作；
- 历史文档记录为什么走到这里；
- 同一事实不能在多个文档里各自演进。

改名不是换包装。它把产品从泛化 API 项目明确成 LLM 网关，也让 Provider、公共协议、调度、额度和治理成为统一词汇。

## 阶段 02：生产纪律变厚，但主线是减少错误自由度

时间：2026-07-19 19:45 到 20:54

关键提交：

- `e43d1ff docs: establish production development discipline`
- `f17ce79 chore: establish architecture and development baseline`
- `5fa5278 docs: define production implementation plan`

这一阶段新增 `dev.md`、环境和 Compose 骨架、架构基线、开发脚本、功能地图与 500 行左右的首份实施计划。文档数量曾经变多，但它们要解决的是同一个问题：Codex 不能看到一个局部缺口就直接打补丁。

开始稳定下来的规则包括：

- 先沿完整请求链调查，再修改局部实现；
- 每个事实只有一个 owner；
- 公共 API、Canonical Model、Provider Adapter 分别拥有不同语义；
- 未知副作用、传输断连和已提交流不能盲目重放；
- 免费池耗尽不能自动进入付费池；
- 当前没有正式生产数据时，错误 schema 和合同直接重建基线；
- `plan.md` 是正式实现期间唯一执行合同。

这一阶段也留下过一个后来反复修正的问题：计划和功能地图过厚时，很容易把“列过功能”误当成“业务已闭环”。后续验收逐渐从文件清单转向真实用户结果。

## 阶段 03：生产网关基础一次落地，宽度很大但还不是交付终点

时间：2026-07-19 23:04

关键提交：`b42652b feat: build production gateway foundation`

这是仓库第一次大规模实现。Go 服务、PostgreSQL schema、Valkey 协调、公共协议、Provider adapter、路由、弹性、额度账本、身份、控制 API、审计、React 控制台和测试框架在同一基础提交中出现。

进入代码的主要 owner：

- `internal/protocol` 与 `canonical`：公共请求/响应与统一语义；
- `internal/providers`：上游 wire、错误和能力；
- `routing`、`admission`、`coordination`、`resilience`：资格、排队、限流、租约、冷却和重试；
- `quota`、`ledger`、PostgreSQL repository：额度与结算事实；
- `identity` 与 control API：管理员、成员、邀请和 Gateway Key；
- `requestflow`：从鉴权、预留到发送、响应和结算的统一执行链；
- React Web：同一后端事实的管理投影。

这一阶段证明架构可以落地，但没有证明真实用户能完成业务。早期前端以宽泛入口、页面内 Tab 和 Playground 组织能力；很多 handler、mock 和组件测试可以变绿，却仍不能代表真实 PostgreSQL、Valkey、Go 进程和浏览器一起工作。

后来保留下来的判断：

```txt
基础代码可以一次铺开，交付只能按真实纵向链路证明。
```

## 阶段 04：真实浏览器成为门槛，Provider 写入开始面对丢响应和并发

时间：2026-07-20 00:25 到 11:27

关键提交：

- `ef53f8e docs: add rapid iteration development skill`
- `2e54bea test: run browser acceptance in headed mode`
- `4369016 fix(web): remove one-sided rails and improve mobile playground`
- `3ad5b92 fix(providers): reject parameterized base URLs`
- `72c08e1 feat(control): persist provider lifecycle safely`
- `d2c4e6e feat(control): harden provider mutation recovery`
- `8dbca35 fix(web): isolate access sessions and invitations`
- `6dbbcc1 fix(web): recover provider lifecycle mutations`

用户在连续 Codex 会话里反复强调：验收标准不是代码存在，而是像真实管理员和成员一样点击、刷新、重登、重启、制造失败，再确认可以继续。

这一阶段从真实失败修了几类根因：

1. Provider Base URL 不能接受会改变请求语义的 query 或 fragment。
2. Provider 创建、修改和启停需要稳定审计、幂等键和并发冲突恢复。
3. 浏览器收到丢失响应时，不能猜测操作失败，也不能生成新请求盲目重放；必须保存原操作并用同一幂等键对账。
4. 邀请码、初始密码和 Gateway Key 只能一次展示，不能进入持久浏览器存储或截图。
5. 管理员与成员会话、邀请和页面状态必须隔离。
6. 移动端不能靠缩小页面容纳桌面表格，页面主体不能横向漂移。

当时还保留 Playground。对它的移动布局改进在当时是合理修复，但 Playground 后来因产品边界收束被整体删除；这说明“曾经修好”不等于“必须永久保留”。

## 阶段 05：从代码清单转向管理员和成员完整主旅程

时间：2026-07-20 到 2026-07-21

相关根会话集中在：

- `继续实现并完成真实验收`
- `完成 LLMGateway 生产级开发`
- `执行真实验收迭代闭环`
- `继续生产级真实验收开发`
- `继续真实浏览器闭环开发`
- `闭合 LLMGateway 核心业务链`

这批会话大多因窗口中断而没有单独 final answer，但派生会话、Git 差异和后续提交能证明实际工作。用户要求的主旅程逐渐固定为：

```txt
管理员初始化
-> Provider / 模型 / 上游 API Key
-> 配置校验与发布
-> 邀请和审核成员
-> 模型授权与额度
-> Gateway Key
-> 成员通过统一 /v1 发起真实请求
-> usage、成本、额度事件和审计可对账
```

这时测试观念也发生变化：

- mock 只做快速确定性边界；
- 真实主旅程连接 Go、PostgreSQL、Valkey 和生产前端；
- 有头 Chromium 同时走桌面与移动；
- 断连、丢响应、重启、并发冲突和重复提交进入同一旅程；
- 页面标题、按钮顺序和组件文件不再被当成稳定业务合同。

最重要的转变不是多写 E2E，而是测试开始保护“用户最终得到什么”和“服务端拒绝了什么”。

## 阶段 06：核心业务闭环，多 Provider wire 和账本边界变成真实证据

时间：2026-07-21 21:17

关键提交：`d4b3e2c feat: close production gateway core`

这一提交把前一日连续会话的工作合并成生产核心。已确认的重要事实包括：

- Provider 凭据模型绑定统一为每模型 `priority` 与 `weight`，删除独立写入旁路；
- OpenAI Chat、Responses、流式、工具调用与 reasoning 经过真实 SDK 和真实 Provider 验收；
- Agnes 与智谱等上游错误按真实 wire 分类，不伪造余额或错误码；
- 请求接受、额度预留、attempt、usage、成本、结算与恢复进入 PostgreSQL 权威事实；
- Gateway Key 只暴露被授权且已发布的公共模型；
- 真实 Provider 返回的额度、限流和 reasoning 行为由 adapter/policy 处理，不散落到公共协议。

真实 Provider 验收曾出现“HTTP 2xx 但 SDK 看不到正文/工具调用”的失败。根因不是 SDK，也不是网关吞内容，而是测试输出 Token 上限过低，Provider thinking 消耗了预算。最终修正测试输入并继续按真实 wire 验证，没有为单一模型加入产品特判。

这次留下的实践：

- HTTP 2xx 只是传输结果，不等于语义完整；
- 真实 Provider 测试必须核对 SDK 可见内容、工具调用、流和 usage；
- 测试夹具错误与产品错误必须分层；
- Provider 外部事实不能靠想象补齐。

## 阶段 07：生产交付硬化，运行、备份和恢复成为产品的一部分

时间：2026-07-21 21:21 到 2026-07-22 07:58

关键提交：

- `93075c4 docs: plan production release and operations`
- `ee48c1e feat: harden production gateway delivery`

核心链通过后，工作转向约 200～300 名受控用户的实际运行边界。新增和硬化的能力包括：

- 可重复构建和验证的发布物；
- Linux/Compose 部署与滚动替换；
- Windows 服务安装、检查和卸载；
- PostgreSQL 加密备份、恢复、清单绑定与腐坏检测；
- Prometheus/运行指标和外部暴露边界；
- 供应链、许可证、secret 扫描和发布归档；
- 容量、故障矩阵、多实例恢复与资源回收；
- 真实 Provider 和官方 Go/Python SDK 验收。

容量验收让“性能”从一个平均值变成多个场景：稳态、突发、长流、热点共享、受控 429、PostgreSQL 连接、Valkey 延迟、内存和 goroutine 回收。热点 32 路长流的共享等待后来单独使用 5 秒首字节 p95 合同，普通请求仍保留更严格边界。

这一阶段证明：部署、备份、恢复和容量不是 README 附录，而是网关能否安全运行的一部分。

## 阶段 08：运营基线收束，Playground 被删除

时间：2026-07-22 22:24

关键提交：`fdf07e5 feat: close operations baseline and define console experience`

Playground 曾经承担页面内请求试验和 Gateway Key 测试，也因此逐渐拥有自己的请求状态、展示和兼容路径。它的问题不是页面不好看，而是容易形成第二套业务入口：真实用户在 Playground 测一次，生产统一 API 再走另一条路径，失败与恢复语义可能分叉。

这次提交直接删除：

- 后端 Playground handler；
- 正式前端 Playground 页面和运行状态；
- Playground E2E 与 mock server；
- 旧独立请求实验入口和兼容转发。

替代关系不是“换一个 Playground”：

- 上游 API Key 在所属行选择已绑定模型，走真实 Provider adapter 做最小生成测试；
- Gateway Key 在 Key 管理处走统一 request workflow 做真实连通性测试；
- 请求结果进入同一 request、attempt、usage、quota 和 audit 事实。

同一提交还建立：

- Provider catalog 预设接入；
- 运营总览聚合；
- 站点资料 PostgreSQL owner 与 CAS；
- 管理员密码与成员账号恢复；
- 上游 API Key 探测；
- Gateway Key 统一 API 测试；
- 发布、备份和恢复入口继续硬化。

Playground 删除后留下的规则：

```txt
测试入口必须回到对象所属任务。
验证统一 API 不能再造第二条业务链。
删除错误 owner 比保留兼容壳更安全。
```

## 阶段 09：`console-concept/` 先验证信息架构，不连接生产事实

时间：2026-07-22

关键代码锚点：`fdf07e5` 中新增的 `console-concept/`

在正式前端仍使用宽泛导航和页面内 Tab 时，先建立了一个完全隔离的控制台概念稿。它借鉴固定版本 Sub2API、New API、LiteLLM、Portkey Gateway、Uni API 和 ylsCode 的任务组织，同时记录许可证边界；AGPL/LGPL 项目只研究机制，不复制源码。

概念稿确认的管理员任务：

- 运营总览、运维监控；
- Provider、模型、上游 API Key、配置发布；
- 成员、邀请、Gateway Key、订阅与额度；
- API 日志、额度记录、上游成本；
- 站点设置。

成员任务收束为：

- 仪表盘；
- 自己的订阅与额度、额度记录；
- Gateway Key；
- API 日志；
- 账号操作。

概念稿明确拒绝支付、充值、通用 OAuth 账号池、粘性会话、iframe、分组、公告、优惠码、兑换码、巨型设置和虚构上游余额。

`console-concept/` 的长期定位是设计参考：可以独立打开，可以继续供人类比较，但不 import 正式模块、不接真实 API、不进入生产构建，也不拥有模拟身份、额度、健康、成本或调度的生产消费者。

## 阶段 10：把概念稿投影回唯一正式控制台

时间：2026-07-22 到 2026-07-23

代码状态：本次提交完成正式控制台投影；`everything` 按新的测试分层留给 owner 的发布候选验收。

用户明确要求以 `console-concept/` 为信息架构合同，但不能复制它的模拟数据或建立第二套业务逻辑。正式实现因此直接重建不合适的模块，而不是在旧 Tab 上叠兼容层。

当前工作树已经完成并经过定向验证的事实：

- capability 投影的管理员/成员共用框架；
- 管理员 14 个直达入口与成员 5 个导航入口；
- 删除 `CatalogTabs`、`AccessTabs`、`LedgerTabs`、旧主题和旧路由；
- 新增管理员运维监控页，读取真实 operations 与上游 API Key 状态；
- 成员可以读取本人 entitlement 与额度记录，服务端强制绑定本人；
- 旧 `/api/control/usage` 重建为 `/api/control/requests` 与 request detail；
- API 日志覆盖 queued、dispatching、streaming、completed、failed、canceled、uncertain；
- Token 未知保持未知，attempt 暴露稳定发送边界但不暴露正文或 secret；
- 成员日志不返回 Provider/上游凭据标签，额度操作者只显示“管理员”；
- Gateway Key 页面统一使用不与上游 API Key 混淆的名称；
- 站点设置只维护真实 site profile，不再放明暗主题；
- 桌面侧栏独立滚动，移动导航可显式打开和关闭，表格不推动页面横向溢出。

有头 Chromium 已在隔离 PostgreSQL、Valkey、真实 Go、生产嵌入前端和 TLS Provider fixture 上通过管理员/成员主旅程。旅程覆盖初始化、密码更换、Provider 并发冲突、丢响应幂等恢复、模型和上游 API Key、发布、邀请、审核、额度、Gateway Key、统一 API、API 日志、站点资料、运维聚合、重启、重登、成员越权拒绝与移动布局。

本次收口后，定向 Go 与前端验证、真实管理员/成员浏览器主旅程、桌面/移动结构和持久状态均已有证据。发布候选的真实 Provider、15 分钟容量、发布物和完整组合仍按各自风险独立运行，不再把 30～60 分钟 `everything` 当成普通界面迭代的默认完成条件。

这一阶段再次确认的原则：

```txt
当重建比补丁更能恢复唯一事实 owner，直接重建。
时间和 token 不是保留错误结构的理由。
重建仍必须闭合权限、持久化、失败、恢复、观测、测试和文档。
```

## 阶段 11：历史文档恢复，历史与当前主干分开维护

时间：2026-07-23

用户要求参考 Kitty 的 `docs/history.md`，在根目录建立 LLMGateway 自己的历史，并要求检索所有 Codex 相关聊天记录。

本次实际完成的证据工作：

- 扫描 Codex 当前 sessions 与 archived sessions；
- 用 session metadata 的 cwd 精确过滤，排除仅在正文提到仓库的其他项目；
- 核验 67 份项目记录、23 份根会话和 44 份派生会话；
- 用 Git 提交、当前文档、计划、长测日志和浏览器证据交叉验证聊天摘要；
- 不读取或记录 `key.txt`、Provider secret、Gateway Key、请求正文和私有配置。

历史文档的作用不是给旧页面找回入口，而是避免重走已经证明错误的路线。未来要恢复某项能力时，仍需先证明当前真实需求、唯一 owner 和安全边界，而不是因为历史上曾经存在。

## 阶段 12：Provider 模板恢复纯写入职责，真实图表与测试门槛一起收口

时间：2026-07-23

用户在真实页面点击 Agnes、Google Gemini、硅基流动和智谱 GLM 的“接入”时都得到 `Registry input is invalid.`。调查发现本机透明代理把权威域名解析到 `198.18.0.0/15` Fake-IP，而模板安装在只创建停用 registry 记录时提前执行了运行时 DNS/SSRF 校验。

最终职责按 owner 重建：

- Provider catalog 在进程启动时校验模板静态 HTTPS 合同；
- 模板安装事务只持久化停用 Provider 和模型，不解析 DNS、不发送请求、不接收 secret；
- 上游 API Key 就地测试和正式发送仍通过 SSRF-safe transport 校验每次解析与重定向；
- Windows 开发入口显式允许 `198.18.0.0/15` 兼容透明代理，生产默认不放宽；
- identity 不根据客户端 IP、Fake-IP 或出站解析结果停用账号或撤销会话。

同一阶段按概念稿和用户截图，把真实查询投影成图表：总览与运维显示 24 小时请求/Token 趋势、终态和错误构成；额度显示当前页已用/剩余 Token；成本按当前筛选页和币种分别显示模型构成。没有复制概念稿模拟数据，也没有新增统计 owner。

一次性有头 Chromium 验收从正式页面完成四个模板安装，并核对停用 Provider、各自模型、四类图表、桌面和 390×844 不溢出；通过后立即删除 Provider 名称、按钮和 SVG 选择器断言。长期浏览器旅程只保留跨边界业务结果。

这轮也暴露两个典型脆弱测试：Provider 审计事件必须恰好 8 条，以及浏览器点击停止后必须每次命中已发送取消。前者被合法新增的四次模板安装打破；后者受浏览器与上游调度时序影响。最终改为只验证主旅程 Provider 必需审计事实与脱敏边界；取消后的 `uncertain` 由 requestflow 可控同步点测试保护，浏览器只证明 Gateway Key 统一 API 成功和 PostgreSQL 正确结算。

长测边界也在这一阶段写清：超过一分钟必须证明短测无法替代的时间或目标环境风险。容量稳态、真实 Provider wire、发布物和完整组合各自有独立目的；`everything` 只用于发布候选，不因普通页面和运营投影变化反复运行。

## 当前结论

LLMGateway 的演进时间不长，但路线已经经历三次重要收束：

1. 从泛化 API 架构收束为两个业务核心。
2. 从“大量代码和页面存在”收束为真实管理员/成员主旅程。
3. 从 Playground、宽泛导航和隐藏 Tab 收束为对象就地测试、直达任务和唯一事实投影。

当前最稳定的请求主线是：

```txt
客户端
-> 公共协议
-> 鉴权与权限/额度
-> admission / 排队
-> 路由与凭据
-> Provider adapter
-> 流式或非流式响应
-> usage / 成本 / 额度结算
-> 日志、指标与审计
```

当前最稳定的管理主线是：

```txt
编辑
-> 校验
-> 持久化
-> 发布
-> 数据面读取
-> 失败对账
-> 回滚或恢复
```

当前最该警惕的旧错误：

- 用 mock 绿灯代替真实用户路径；
- 用 Playground 或兼容入口制造第二套请求事实；
- 用隐藏 Tab 和帮助文案掩盖任务不可发现；
- 用页面自己计算额度、健康、成本或调度；
- 用虚构上游余额或“看起来正常”填补未知；
- 在丢响应和未知副作用边界盲目重放；
- 为了兼容保留错误 schema、旧路由或双轨实现；
- 为菜单、标题、按钮和文件形状增加长期测试；
- 把一次测试通过写成整个产品完成。

当前最证明有效的实践：

- 一个事实一个 owner；
- Provider/model/credential 分离；
- priority 决定管理员层级，weight 只在同级分配；
- 免费与付费资源域严格隔离；
- PostgreSQL 拥有持久业务事实，Valkey 只拥有短期协调；
- request/attempt/usage/ledger/audit 能跨重启对账；
- 未知副作用进入 `uncertain`，恢复不盲目重放；
- 管理员与成员共用框架，但服务端权限独立强制；
- 上游 API Key 与 Gateway Key 在所属页面走真实链路测试；
- 桌面与移动复用同一任务；
- 日常定向测试与真实 Provider、容量、发布和灾备验收分层；
- `plan.md` 是当前执行合同，`spec.md` 是当前事实主干，`history.md` 只保存演进证据。
