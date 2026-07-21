# LLMGateway 生产发布与运营闭环计划

## 需求文档

LLMGateway 已建立双核心与最低生产地基的可运行基线；本轮真实 Provider 验收发现凭据路由策略尚未从管理员入口贯通到发布数据面，必须先闭合这一核心缺口。随后把产品交付为创业公司能够长期运营、升级、排障和对客户负责的正式生产服务：真实 Provider 合同经过现场核验，200～300 名受控用户的容量有数据依据，部署、升级、回滚、备份、灾备、安全供应链、发布物、账号恢复、成本归集和支持文档形成完整闭环。

产品仍围绕多 Provider 统一 API 与管理员/成员系统演进。新增工作必须服务真实接入、生产运行或公司经营，不建立独立于双核心的第三条产品主线。

## 当前事实

- 当前提交 `d4b3e2c` 已通过唯一完整验证：Go、sqlc、前端、mock/真实有头 Chromium、migration、控制面、核心链、跨实例/强杀恢复、主密钥轮换、PostgreSQL 备份恢复、Windows amd64 生产运行和构建矩阵。
- 公共 API 已覆盖 Models、Chat Completions、Responses、非流、流式、工具、reasoning、usage 和后台 Responses。
- 管理员/成员、邀请审核、模型权限、额度、RPM/TPM/并发和 Gateway Key 已闭环。
- Provider catalog 已集中拥有 kind、展示名称和 adapter builder，并被请求、探测、校验和管理端消费。
- 当前 Provider 行为主要由官方资料研究、单元 wire fixture 和隔离 TLS Provider 证明，没有使用真实 GLM/Agnes 生产凭据完成本轮现场验收。
- 2026-07-21 已使用 owner 提供且未输出的专用测试凭据直接探测官方模型目录：Agnes `/v1/models` 返回 5 个模型，智谱 `/api/paas/v4/models` 返回 8 个模型；`agnes-2.0-flash` 与 `glm-5.2` 当前存在。该证据只证明凭据、端点和模型目录，不证明经过 LLMGateway 的 chat、stream、tools、reasoning、usage、错误和取消合同。
- 当前固定参考版本为 LiteLLM `bd44c9e`（核心 MIT，enterprise 另行授权）、New API `5a6c53d`（AGPL-3.0）、Portkey Gateway `669825c`（MIT）、Sub2API `d4b9797`（LGPL-3.0）和 Uni API `20da7a7`/`v1.7.191`（Apache-2.0）。只吸收真实 canary、输出提交前重试、Provider/Key 冷却、流清理和低基数观测机制；不复制 AGPL/LGPL 源码，不采用盲切渠道、模糊模型匹配或把商业系统揉进请求核心链的设计。
- 同一 owner 的前身项目 Kitty `341ce17`（MIT）及其 `ref/repos` 已作为额外 Provider 证据复核；Kitty 的 Provider/model 方言投影、只读 models 探测、Agnes 长响应上限、智谱 preserved thinking 和稳定错误码适合本项目，Agent session/context/tool runtime 不属于网关 owner，不能复制进 LLMGateway。
- owner 当前提供 Agnes 与智谱各三条、Google Gemini 一条专用测试凭据。Agnes 三条均通过真实 models/chat，其中 `agnes1` 已通过 models/chat/stream/tools/reasoning；智谱三条 models 均成功，`智谱2` 已通过 chat/stream/tools/reasoning，`智谱1` 与 `智谱3` 稳定返回 HTTP 429 / `1113` quota，可用于真实冷却与候选切换验收。
- 2026-07-21 已依据 Google 官方 OpenAI compatibility 文档核验 `https://generativelanguage.googleapis.com/v1beta/openai/`、`reasoning_effort` 与当日模型目录，并复核 Kitty `8270adbd`（MIT）、LiteLLM `bd44c9e`（核心 MIT）、Portkey Gateway `669825c`（MIT）及 New API `5a6c53d`（AGPL-3.0）的 Gemini 机制。可采用的是独立 Provider policy、thought signature 无损回放、结构化 quota/retry 和工具 schema 收敛；拒绝复制 AGPL 源码、原生 Gemini 全协议转换层和散落在公共请求链的厂商分支。
- Gemini 专用凭据通过官方 OpenAI-compatible `/models`，`gemini-3.5-flash` 当日存在；首次 chat/tools 返回 HTTP 503，stream 在约 60 秒后传输中断。该证据证明凭据、端点和模型目录有效，也证明生成链仍需完成专用 adapter、有界重试和真实成功/失败验收，不能写成已支持。
- 2026-07-21 已依据硅基流动官方 Chat Completions 文档核验 `https://api.siliconflow.cn/v1`、文本流式、工具与 reasoning wire，并复核 Portkey Gateway `669825c`（MIT）和 New API `5a6c53d`（AGPL-3.0）的 SiliconFlow 实现。文本合同直接复用 OpenAI-compatible；图片、Embedding、Rerank 和 FIM 属于其他协议 owner，不因供应商同名混入当前文本切片，也不复制 AGPL 源码或参考项目的占位 Responses 转换。
- 2026-07-22 依据硅基流动当日官方 Chat Completions 合同重新核验 Qwen3.5 推理控制：`Qwen/Qwen3.5-9B` 使用 `enable_thinking` 与 `thinking_budget`，`reasoning_content` 和最终 `content` 分离；`reasoning_effort` 当前只适用于 `deepseek-ai/DeepSeek-V4-Flash`。因此通用 OpenAI-compatible 模型不能再把一个 `reasoning` 布尔值同时解释为 toggle 与 effort，具体控制方式必须由模型 capability profile 拥有。
- owner 提供的首批 11 条硅基流动凭据均在官方 `.cn /models` 返回 403；随后单独提供的新测试凭据已用 `Qwen/Qwen3.5-9B` 通过 `/models`、chat、stream、tools、reasoning 和 authoritative usage，全部 HTTP 200。当前事实是首批凭据不可用、新凭据可用，不把凭据数量或代金券说明伪造成可用额度。
- `cmd/providercanary` 已建立外部 secret 标准输入、显式 DNS allowlist、场景选择和脱敏 JSON 证据；它复用唯一 Provider Catalog、canonical parser 与 SSRF-safe transport。真实 canary 已修复专用 adapter 缺 models 探测、兼容流 no-op chunk 和智谱流不返回 request ID 三个 wire 根因。
- 凭据路由绑定已统一为 `modelBindings[{modelId, priority, weight}]`：控制 API、幂等指纹、事务仓储、审计、管理端、配置捕获和数据面消费同一事实，旧的独立绑定旁路已删除。2026-07-21 的隔离控制面与真实有头 Chromium 旅程证明创建、响应丢失、刷新对账、编辑和发布后，PostgreSQL live binding 与 active revision 均保留管理员提交的 `10:80,20:30`。
- 2026-07-22 生产前端、真实 Go、PostgreSQL、Valkey 与有头 Chromium 主旅程再次退出 0；管理员创建的 toggle 推理档案同时存在于 live model 与 active revision，Playground 直接消费 `reasoningMode`，按 toggle/effort/hybrid 生成对应公共请求，成员权限旅程仍通过。隔离浏览器、Gateway 与 fixture 进程已清理，只保留脱敏截图证据。
- 2026-07-22 双实例 60 秒容量快速门槛退出 0：300 个独立 active 成员/Key/额度，60 人混合稳态 5,390/5,390 成功，p95 1.055 秒、p99 1.061 秒、首字节 p95 80 ms；300 人同步突发为 175 成功、125 个带 `Retry-After` 的受控 429，全部用户在 0.93 秒内取得终态；单用户 32 路长流热点按额度并发得到 8 成功、24 个受控 429。两个 Gateway PostgreSQL 池分别不超过 24，观测连接 1，Valkey p95 1.08 ms；goroutine 合计从 26 到峰值 1,168、客户端断开后回落至 34，RSS 峰值 275 MB、最终 149 MB。5,742 条 request/reservation 全部 settled 或 released，运行日志零 ERROR 且不含测试 secret，隔离进程和容器已清理。
- 同日故障与崩溃快速门槛退出 0：超长上下文返回 400 且 Provider 请求增量为 0；客户端首事件后取消、通用 OpenAI-compatible 503、首事件后畸形流和传输断连各只有 1 个 attempt，均保存 `uncertain+reserved`，其中未提交响应返回带 `Retry-After` 的 409；429 首次明确拒绝后只切换 1 次并成功，持久 request 为 completed/settled、attempt 恰好 2。随后实例一持有 128 条已提交流并占满共享 Provider 并发时被强杀，128 个客户端全部先收到 200 后中断；实例二先返回受控 429，再在租约 TTL 到期后恢复 200，PostgreSQL 恰好恢复为 128 个 `uncertain+reserved`，未重放上游。全程运行日志零 ERROR、零测试 secret，隔离资源已清理。
- 调整域级 RPM 后，900 秒正式稳态与后续全部故障/强杀阶段在 940 秒内退出 0：80,499/80,499 个稳态任务完成，非预期错误为 0；p50 70 ms、p95 1.056 秒、p99 1.060 秒、首字节 p95 79 ms。稳态包含 52,328 个短 Chat、12,073 个短流、8,048 个长流、4,023 个工具/reasoning 和 4,027 个 background Responses；300 人突发 178 成功、122 个受控 429。两个 Gateway 的 PostgreSQL client backend 峰值严格为 24+24、观测端 1，Valkey p95 1.09 ms；goroutine 合计 26→峰值 1,074→26，RSS 峰值 260 MB、最终 140 MB。80,856 条持久 request 全部终态，80,706 settled、146 released、仅 4 个预期 uncertain hold；日志零 ERROR、零测试 secret，隔离资源已清理。
- 独立 extended-stream 阶段进一步用 60 个用户各保持 600 个 SSE 事件约 30.36 秒，60/60 完成且首字节 p95 小于 2 秒；Provider 峰值并发 60，阶段结束及故障/强杀后 goroutine 回落至 29，RSS 最终 149 MB。该证据与 15 分钟中重复的 8,048 个约 1 秒流互补，不再用短事件流冒充真实长连接。
- 当前验证证明功能正确性与关键并发边界，没有代表 200～300 名用户真实流量分布的容量、长流稳态和资源基线。
- 2026-07-22 正式部署主旅程用最终源码重建 release/candidate Linux 镜像后退出 0：PostgreSQL 18.4、Valkey 9.1.0、两个 UID 65532/read-only/cap-drop Gateway 和 Caddy 2.10.2 internal TLS 全部健康；独立 migration 建立 baseline，首次有头 Chromium 完成管理员初始化、邀请/审核成员、成员 URL/API 越权拒绝。升级前 custom dump 可列出；无效数据库 migration 被隔离且存量 TLS 服务继续 200，无效主密钥版本只击穿 gateway-a、gateway-b 继续承接；随后逐实例替换到不同 candidate image、按存储→Gateway→Caddy 顺序停启、把 dump 恢复到 `llmgateway_restored` 并逐实例切 DSN，第二次有头 Chromium 证明管理员/成员身份与权限仍在。聚合报告全部恢复项为 true，容器环境和全栈日志均不含运行时 secret，隔离容器、卷、网络已清理。
- 同日 Windows SCM 隔离旅程退出 0：生产 `webembed` 二进制先消费 file secret 通过 `--check-config` 和独立 migration，再以 `NT SERVICE\\LLMGateway` 虚拟账户真实安装；延迟自动启动、Event Log JSON、HTTP readiness、SCM stop 对应 graceful shutdown、再次启动和两次有界失败重启策略均通过，随后服务、Event source、隔离 PostgreSQL/Valkey 和 secret 目录已清理。
- 2026-07-22 部署完成后再次运行 `scripts/test-provider-real.ps1`，352 秒后完整退出 0：Agnes、智谱、硅基流动、Go/Python SDK、quota 排除和健康 priority 接管继续通过；Gemini 仍在有界显式重发后返回 503 `upstream_circuit_open`，只证明 degraded 错误合同。Provider catalog 现由每个 definition 唯一拥有权威引用、合同快照日期、现场验证日期、参考供应商/模型、现场能力和 `verified/degraded` 状态；控制 API 与文档矩阵投影同一事实，Gemini 不被 fixture 伪装为 verified。
- 2026-07-22 本次接管后重新运行 `scripts/test-provider-real.ps1`，258.5 秒完整退出 0：Agnes、智谱、硅基流动、Go/Python SDK、quota 排除和健康 priority 接管继续通过；Gemini 真实完成带 thought signature 的工具调用与签名工具回放，返回权威 usage。隔离 Provider/Gateway 进程与本轮 PostgreSQL、Valkey 均已清理，因此 Gemini catalog 现场状态可由 `degraded` 更新为 `verified`，但该单次成功不构成永久可用性承诺。
- 2026-07-22 部署切片固定参考机制：Docker Compose 官方 secret 文件挂载与 healthcheck 依赖、Caddy 官方自动 HTTPS/流式反向代理、Go `x/sys/windows/svc` 原生 SCM 生命周期；并复核 LiteLLM `bd44c9e`（MIT）的非 root/read-only/cap-drop hardened Compose 与 Sub2API `d4b9797`（LGPL-3.0）的 migration/回滚约束。只采用机制，不复制第三方源码。主部署目标确定为 Linux Docker Compose 的双 Gateway、独立 migration job、PostgreSQL、Valkey 与 Caddy TLS；Windows 使用同一发布二进制作为原生 SCM service。正式域名、证书、生产主机和外部 secret store 未提供，现场验收使用隔离 Caddy internal CA，不伪造公网部署。
- 当前有 HTTP/Go 进程指标、结构化日志和 readiness，没有 admission、Provider、额度、恢复和后台任务的完整领域指标与运行手册。
- 2026-07-22 切片 4 调查确认当前 Prometheus 只有 HTTP route/status/duration 与 Go/process collector，无法区分 admission、Provider attempt、quota operation、request recovery 和 background Responses；生产 Caddy 还会把 `/metrics` 与用户 API 一起代理到公网。Identity store 已有内部 `RevokeUserSessions` SQL，但没有受权限/审计的控制 API；管理员成员密码恢复、唯一管理员离线恢复和 Gateway Key replacement 均无 owner 命令或真实旅程。
- 2026-07-22 切片 4 可观测闭环：runtime metrics 唯一拥有 admission 等待/拒绝、协调租约、Provider attempt、quota operation、request recovery 与 background Responses，异常终态同时输出不含 raw error/用户/Key/模型/URL 的稳定 `event`；正式部署复验退出 0，`deployment-report.json` 证明公网 `/metrics` 为 404、backend 可逐实例抓取领域指标、两次有头浏览器和升级/恢复旅程仍通过。官方 GitHub release API 核验 Prometheus `v3.13.1` 与 Grafana `v13.1.1`；`scripts/test-observability.ps1` 校验官方 SHA256 后由 promtool 接受 6 条规则，并经 Grafana HTTP API 导入/读回 UID `llmgateway-operations` 的 6 个 panel。外部监控主机、通知渠道和正式 runbook 域名仍是部署环境依赖，不伪称核心 Compose 已接入值班。
- 2026-07-22 切片 4 账号恢复闭环：成员密码重置使用 CSRF、peppered idempotency fingerprint 与单个 PostgreSQL 事务更新 Argon2id hash、撤销全部成员 session、完成 mutation 并写 audit；管理员批量撤销自己的其他 session 时保留当前 session。隔离 PostgreSQL 测试、真实生产构建有头 Chromium 和 `scripts/test-operations.ps1` 均退出 0，分别证明重放只产生一个 mutation/audit、旧成员会话与旧密码失效、新密码登录、操作者会话保留，以及 `dbtool` 无确认开关拒绝、password file 确认后只恢复 administrator、撤销旧 session 并写 system actor audit，输出不含密码。
- 2026-07-22 切片 4 Key replacement 闭环：服务规范化旧 Key 多模型绑定，repository 在创建事务内锁定并复核 active 旧 Key 的 owner、模型范围和到期时间；同 idempotency key 可重新派生同一明文但 PostgreSQL 只存 digest、prefix 和无 secret result。真实有头 Chromium 主旅程主动丢失已提交 replacement 回执，再以同 key 收敛；新旧 Key 同时通过 `/v1/models`，撤销旧 Key 后旧 Key 401、新 Key继续 200，数据库核对两条 mutation/绑定、replacement/revoke audit 且浏览器持久状态和日志无完整 secret。
- 2026-07-22 `scripts/test-supply-chain.ps1` 在当前完整工作区退出 0：govulncheck 可达漏洞为 0，pnpm high/critical audit 为 0，Go/Node 许可证策略通过，Gitleaks 对 Git 非忽略源树未发现 secret；Trivy 对正式 scratch 镜像内三个 Go 二进制未发现 high/critical，并确认 Dockerfile 与部署 IaC 没有 high/critical misconfiguration。固定扫描工具均从官方 release 获取并核对 SHA-256，隔离扫描镜像与工作目录已清理。
- 2026-07-22 `scripts/build-release.ps1 -Version 0.1.0-acceptance6` 与独立 `scripts/verify-release.ps1` 连续退出 0：Windows/Linux amd64 三个发布二进制双构建 SHA-256 一致，OCI 镜像 tar、SPDX/CycloneDX SBOM、Go/Node 许可证清单、版本 manifest、SHA256SUMS 和 in-toto/SLSA provenance 均生成并交叉校验；本地临时 Cosign 身份使用无透明日志服务的显式 signing config，私钥随工作目录清理，公钥验签 bundle 通过且未向公共 Rekor 写入。正式 tag CI 仍需验证 GitHub OIDC keyless 身份路径。
- 2026-07-22 `go test -race ./internal/...` 与 actionlint v1.7.12 均退出 0，GitHub Actions 使用的 checkout/setup-go/setup-node/pnpm/upload-artifact commit 与各官方 major tag 当前引用一致，PostgreSQL 18.4 与 Valkey 9.1.0 CI service 也已固定到本轮官方镜像 digest。`0.1.0-acceptance7` 再次通过完整构建/独立签验，且 Windows/Linux 两个交付 zip 均实际包含 `RELEASE.md`；该文档准确列出发布范围、升级/回滚、制品验证、已验证平台和外部依赖。
- 当前 CI 执行 Linux/Windows 验证和 race，没有依赖漏洞、secret、许可证、SBOM、镜像、制品签名和发布 provenance 门槛。
- 当前有显式备份、恢复和主密钥轮换命令，没有定时备份、异地保留、恢复周期、RPO/RTO 和整站灾备演练。
- 当前成员可以登录、注销和被停用，没有管理员驱动的密码恢复、管理员锁定恢复和会话批量撤销运维流程。
- 当前账本记录 token 与 usage 来源，没有 Provider 价格版本、请求成本快照、客户合同额度对账和毛利所需的只读经营事实。
- 2026-07-22 切片 6 固定灾备机制：PostgreSQL 18 官方 `pg_dump`/`pg_restore` 合同要求 custom-format 备份可列出并只恢复到新数据库；Restic v0.19.1（BSD-2-Clause，未归档）提供加密仓库、远端 backend、快照校验与 daily/weekly/monthly retention，正式镜像固定为 `restic/restic@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510`。主机备份每 6 小时执行，目标 RPO 为 6 小时、目标 RTO 为 2 小时；Restic 密码与数据库/主密钥备份分开保管，普通启动和升级不自动初始化、清空或覆盖仓库。
- 2026-07-22 `scripts/test-disaster-recovery.ps1` 在 Docker Desktop Linux 文件系统中直接执行正式 Bash/Compose/Restic 路径并退出 0：首次有头 Chromium 建立管理员与成员后，加密备份保存 PostgreSQL custom dump 和配置，执行 7 daily/5 weekly/12 monthly 与 100% pack check；随后删除源 Gateway/Caddy/PostgreSQL/Valkey 容器、网络、卷和配置，从独立 Restic 控制凭据恢复到空目录和新数据库，第二次有头旅程证明身份与成员 403 边界保持。实测恢复点年龄 0 秒、恢复 28 秒，低于 6 小时 RPO/2 小时 RTO；无确认、错误密码、非空目录、重复数据库恢复和损坏 pack 均被拒绝，日志无测试 secret。该证据不冒充 owner 尚未提供的正式异地 backend、生产 systemd 主机或 DNS/TLS 切换。
- 同日成本机制复核 New API `5a6c53d`（AGPL-3.0）的 pre-consume frozen billing snapshot、Sub2API `d4b9797`（LGPL-3.0）的事务结算与 Uni API `20da7a7`（Apache-2.0）的 usage 聚合。可采用的是“请求接受时冻结版本，结算时只消费该快照”；拒绝复制 AGPL/LGPL 源码、Float 金额、表达式引擎、默认虚假价格和随后改价重算历史。LLMGateway 只实现当前文本 input/output token 单价，使用 ISO 货币代码与整数 currency nanos/百万 Token，未知价格在 Provider 发送前 fail closed。
- 2026-07-22 成本 owner 已闭环：`model_price_versions` 由数据库触发器禁止 UPDATE/DELETE，独立幂等 mutation 创建版本并审计；quota acceptance 同一事务冻结版本/币种/费率，settle/compensate 对 input/output Token 分别向上取整到 currency nano 并保存总成本，release/uncertain 不写金额。核心真实链证明缺价返回 409 且 Provider 增量/请求行均为 0、重放收敛、冲突拒绝、后续改价不漂移历史、管理员聚合可读、成员成本 API 403 且个人 usage 不泄露采购价；60 秒双实例 300 用户容量回归和生产前端有头成本表单均退出 0。
- 同日硅基流动官方价格页核验 `Qwen/Qwen3.5-9B` 为 CNY 1.5/12 每百万 prompt/completion Token；真实 Provider/Go SDK 产生 9 个 authoritative 请求、677 input/576 output Token，逐请求定点结算为 7,927,500 nanos，管理员聚合与 PostgreSQL 请求快照一致。owner 未提供客户销售合同费率，毛利样例保持 `not_provided`，不以假设售价冒充完成。

## 失败证据

- 无真实 Provider 凭据时，无法证明当前 adapter 与供应商当日生产 wire、限额和错误完全一致。
- 2026-07-21 最小真实 chat 采样中，Agnes `agnes-2.0-flash` 返回 HTTP 200、标准 choices 与 usage；智谱三条凭据中一条成功、两条返回 HTTP 429 / `1113`；Gemini 目录成功但生成请求返回 503 或传输中断。当前仍缺三家 Provider 经过真实 Gateway 调度后的完整成功、冷却、切换和恢复证据。
- 2026-07-22 从 `scripts/test-provider-real.ps1` 首次复跑稳定复现：Go SDK 经硅基流动完成 models/chat/stream/Responses/tools/reasoning/cancel/error；Python SDK 经 Gemini 的 chat 在四次显式、遵守 `Retry-After` 的独立重发后仍为 HTTP 409 `upstream_outcome_uncertain`，紧接的首事件前流失败被错误覆盖为 HTTP 503 `uncertain_state_failed/storage_unavailable`。隔离日志已核验不含 Provider/Gateway secret，进程与本轮容器均清理完成。
- 上述 503 的已确认根因位于 requestflow 的流式未知发送边界：尚未收到首个事件时 `streamState.usageSource` 是零值，`finishBrokenStream` 却把它作为 usage 写入 PostgreSQL，违反 `usage_source` 枚举合同；该路径必须保持 request/attempt 为 `uncertain`、保留额度预留并向客户端返回带 `Retry-After` 的 409，不能被持久化参数错误覆盖。
- 2026-07-22 修复上述 usage 零值后，`scripts/test-core.ps1` 已用真实 PostgreSQL/Valkey/Provider fixture 证明 HTTP 409、`uncertain` attempt/request、unknown reservation 与不重放边界；第二次真实 Provider 复测在 Go SDK 的硅基流动流场景失败为 `stream_content_missing`。网关返回约 53 KB 成功 SSE，SDK 收到 chunk/choice 但没有最终 `content`；结合官方合同，根因是 Qwen3.5 默认推理在 256 输出 Token 内只产生 `reasoning_content`，而当前模型事实无法让普通请求显式关闭 thinking。
- 2026-07-22 引入 reasoning profile 后第三次真实复测证明硅基流动 Go SDK 的 models/chat/stream/Responses/tools/reasoning/cancel/error 全部通过；Gemini 首个 chat 返回明确 HTTP 429，唯一凭据随即冷却，后续请求得到不带 `Retry-After` 的 `free_pool_unavailable`。Routing 已拥有冷却排除的 `AvailableAt`，但 requestflow 丢弃该事实；Python 显式复验客户端也错误地只按错误 code 白名单判断 429，二者必须分别在 owner 处修复。
- 2026-07-22 `scripts/test-provider-real.ps1` 最终完整退出 0：真实 Agnes 专用工具/reasoning、Agnes 通用兼容 chat、硅基流动 models/chat/stream/Responses/tools/reasoning/usage/cancel、Go v3.44.0 与 Python openai 2.46.0、智谱两条 HTTP 429/1113 quota 凭据冷却和第三条健康凭据按 priority 接管均通过；隔离进程、PostgreSQL 与 Valkey 已清理。Gemini models 成功，但专用 tool-call 在四次遵守 `Retry-After` 的显式复验后仍为 HTTP 503 `upstream_circuit_open`，本轮只证明 bounded retry/degraded 错误合同，不把 Gemini 生成写成当轮成功。
- 同日后续复验中 Gemini 的专用工具调用和签名回放均真实成功，thought signature、工具 schema 收敛、reasoning effort 与 usage 经 LLMGateway wire 正向证明；此前 503 仍保留为有界重试和 degraded 恢复证据，不再代表当前现场状态。
- 同日真实浏览器首次复验在 Playground 稳定复现 `unsupported_capability`：模型目录只下发粗粒度 reasoning 布尔值，前端把 toggle 模型错误投影为 `reasoning_effort`。根因修复后，目录下发 registry 拥有的 `reasoningMode`，Playground toggle 默认关闭、effort 使用强度、hybrid 仅在开启时附加强度；回到同一有头 Chromium 旅程后通过。
- 无容量与长流测试时，无法给出 200～300 名用户下的并发、连接池、队列等待、数据库、Valkey、内存和文件描述符配置基线。
- 2026-07-22 首次双实例容量入口完成 300 个独立成员/Key/额度、60 人 60 秒混合稳态、300 人突发与单用户热点：稳态 5,377/5,377 成功，p95 1.057 秒；突发 163 成功、137 个带 `Retry-After` 的受控 429，p95 0.829 秒；5,729 条 PostgreSQL request/reservation 全部终态，Valkey p95 1.11 ms。失败门槛是 Gateway goroutine 从合计 26 增至 11,462、RSS 达 453 MB 且 3 秒不回落；根因是 `ProviderFactory.Client` 每次 attempt 新建独立 `http.Transport`，每条上游 keep-alive 连接保留 read/write loop。双实例 background recovery 同时扫描终态时还把已被另一实例收敛的 `ErrNotFound` 误记为 ERROR。报告不含运行时 secret，隔离进程和容器已清理。
- `ProviderFactory` 现按进程复用唯一 SSRF-safe client/transport，仍在每次 RoundTrip 重新验证 URL 与 DNS；同一容量入口使最终 goroutine 从 11,462 降至 34、RSS 从 453 MB 降至 149 MB。Background recovery 把另一实例已收敛的 `ErrNotFound`/`ErrConflict` 作为幂等 ownership loss，复测无 ERROR；连接采样按 PostgreSQL `application_name` 和 client backend 分解，排除了 autovacuum 对池预算的干扰。
- 120 秒持续速率复验制造出容量桶真实拐点：60 人流量约 93 RPS，resource/model/provider 默认 3,000 RPM 只能持续补充 50 RPS；初始 3,000 token 桶耗尽后，11,192 个稳态任务中 2,073 个直接 429，另有 133 个 background Responses 已接受但执行期失败。该失败与 Provider、成员、Key 或额度无关，owner 是网关域级默认容量；目标 profile 将 global 调整为 12,000 RPM，resource/model/provider 调整为 9,000 RPM，保留 user 600 RPM、Gateway Key 300 RPM、entitlement、credential 和全部并发硬限制，随后必须通过 15 分钟持续稳态证明。
- 上述 900 秒正式稳态已证明 12,000/9,000 RPM profile 在目标流量下没有持续速率拒绝；300 人同步突发仍受 128 Provider 并发硬边界保护并返回可恢复 429，未用吞吐目标放宽公平、额度或外部凭据限制。
- 无可安装服务与升级演练时，无法复现正式主机重启、自启动、滚动升级、migration 前置、回滚和日志归档流程。
- 部署调查确认当前生产配置只能从进程环境读取数据库 DSN、Valkey 密码、主密钥和 pepper，Compose secret 文件无法被唯一配置 owner 消费；当前 Windows 二进制也没有 SCM handler。现有 `scripts/test-operations.ps1` 只证明手工启动、单次备份/恢复和前端嵌入，不能证明 TLS、安装、自启动、双实例滚动替换或失败回滚。
- 首次正式 Docker 镜像构建在 Node 22.13 内置 Corepack 验证 `pnpm@10.33.0` 时失败：Corepack 只带旧签名 key，无法验证当前 pnpm 发布签名。发布 Dockerfile 改为从 npm 安装 `web/package.json` 已精确锁定的 pnpm 10.33.0，不通过关闭完整性或签名检查绕过供应链失败。
- 无供应链与发布门槛时，构建成功不能证明依赖、镜像、secret、许可证和发布制品满足正式发布要求。
- 无定时异地备份与 RPO/RTO 演练时，单次本机 restore 不能证明主机或卷损坏后的业务恢复能力。
- 无账号恢复流程时，遗忘密码或唯一管理员锁定需要临时数据库操作，不能作为长期支持边界。
- 无成本快照时，usage 能说明消耗量，但不能稳定解释某次请求使用了哪个价格版本和产生了多少成本。

## 最终目标

- 智谱 GLM、Agnes、Google Gemini 与硅基流动四家真实供应商覆盖 `zhipu`、`agnes`、`gemini` 和 `openai-compatible` 四种 adapter kind，通过真实凭据、标准 SDK 和隔离 canary 验收，形成可维护兼容矩阵；硅基流动当前只纳入文本 Chat/Responses/流式/工具/reasoning，其他模态按后续真实需求独立扩展。
- 以代表性聊天、Responses、工具、reasoning、短流和长流流量证明 200～300 名受控用户的容量基线、限流、公平、恢复和资源上限。
- 交付一个可安装、可自启动、可升级、可回滚、可备份和可排障的正式部署目标，并完成 staging 到 production 的演练。
- 关键领域状态具有低基数指标、结构化日志、运行看板和可行动阈值，支持值班人员定位 Provider、容量、额度和恢复故障。
- CI 与发布链生成经过漏洞、secret、许可证和 SBOM 检查的确定性制品，并附校验和、签名/provenance 与发布说明。
- 备份按计划加密并异地保留，恢复演练满足明确 RPO/RTO；主密钥、数据库和整站恢复顺序有可执行手册。
- 管理员可以安全恢复成员访问并处理管理员锁定，所有恢复操作撤销旧会话并留下非秘密审计。
- 每次请求保存明确的 Provider 价格版本和整数最小货币单位成本，支持按客户合同与资源域核对用量和毛利。
- 用户接入、管理员运营、Provider 扩展、部署、升级、备份、恢复和事故处理文档与实际命令一致。

## 不做范围

本计划只补齐正式发布与长期运营所需事实；任何新产品能力仍需真实用户需求、单一 owner 和独立生产级验收。

## 设计

### 真实 Provider 合同

- Provider Catalog 继续拥有 kind 与 builder；每个 adapter 增加可追溯的官方合同版本、已验证模型、能力和最后验证时间。
- 通用 OpenAI-compatible wire、canonical 转换、流解析和 usage 只实现所有已支持 Provider 共有的合同；Agnes、智谱与 Gemini 的 reasoning、工具 metadata、错误和能力差异分别由独立 Provider policy 文件拥有，公共协议、调度和账本不得按 Provider 名称分支。
- Gemini 工具调用把 Google thought signature 作为受约束的 opaque metadata 经 Public Protocol、Canonical Model 和 Gemini adapter 无损返回与回放；缺失必需 signature 时发送前明确拒绝，不静默丢失或伪造。
- 真实 canary 使用专属低权限测试凭据与最小额度，夹具只保存脱敏 wire 结构；真实 secret 只来自外部 secret 注入。
- 标准 SDK 验收至少覆盖 Go 与 Python 客户端的 models、chat、responses、stream、tools、reasoning、取消和错误解析。
- Provider 能力变化先更新 adapter/capability owner，再同步管理端和兼容矩阵。
- 模型能力同时保存受校验的 reasoning 控制 profile：`toggle` 表示公共 `thinking` 开关并由兼容 adapter 映射为 `enable_thinking`，`effort` 表示 `reasoning_effort`，`hybrid` 表示两者均可表达；无 reasoning 的模型不保存该 profile。数据面按 profile 构建 adapter，公共协议和 requestflow 不出现供应商名分支；toggle 模型在普通请求未显式要求 reasoning 时发送 `enable_thinking=false`，避免默认推理吞掉全部可见输出预算。

### 凭据路由绑定

- `registry.CredentialModelBinding` 是凭据可路由模型、管理员优先级与同级权重的统一控制面事实；创建、编辑、幂等指纹、审计、展示、发布和数据面均消费这一结构。
- 控制 API 只接受 `modelBindings[{modelId, priority, weight}]`，每个模型只能绑定一次；优先级范围 `0..1000`，权重范围 `1..1000`，与路由器资格校验一致。
- 创建与编辑凭据在同一个 PostgreSQL 事务中验证 Provider、资源域和全部绑定，再原子替换；不存在独立绑定旁路、旧 `authorizedModelIds` 凭据合同、双读写或兼容层。
- 管理端在每个已选择模型旁直接编辑 priority/weight，待确认操作保存完整非秘密绑定事实，刷新对账时按规范化绑定比较，不保存 Provider secret。

### 容量与稳态

- 建立 300 个独立 active 成员、Gateway Key 和模型额度的可重复流量模型；稳态按 20%（60 人）并发活跃，流量由 65% 短 Chat、15% 短流、10% 长流、5% 工具/reasoning 和 5% 后台 Responses 组成，另执行全部 300 人同步突发与单用户热点。隔离 Provider 固定短请求延迟、长流事件数和间隔，使测量结果不受真实供应商额度与网络抖动支配。
- 测量 p50/p95/p99、排队等待、首字节、吞吐、错误、内存、goroutine、连接池、数据库锁与 Valkey 延迟。正式容量旅程要求非预期错误为 0、每名用户均取得终态、短请求 p95 小于 2 秒/p99 小于 5 秒、流首字节 p95 小于 2 秒、长流 p95 在 10 秒内完成、300 人突发在 30 秒内全部成功或得到带恢复信息的受控拒绝；PostgreSQL 连接不越过显式池上限，Valkey 本机 p95 小于 25 ms，稳态结束后 goroutine、RSS 和协调租约回落且 PostgreSQL 账本与成功终态一致。
- 负载工具只调用隔离 Provider；通过可控延迟、429、5xx、断流和强杀制造容量与恢复压力。
- 本机快速门槛运行不少于 60 秒，正式验收运行不少于 15 分钟；另提供 12 小时 soak 参数供发布候选持续门槛。形成默认配置、单实例建议容量、扩容信号和最大安全边界，不用无限重试或放宽 fail-closed 换取吞吐。

### 部署与升级

- 正式主拓扑是 Linux Docker Compose：两个无状态 Gateway 实例只监听内部网络，PostgreSQL 与 Valkey 不发布宿主端口，Caddy 是唯一 TLS 入口；Gateway 镜像使用固定 digest/发布标签、非 root 用户、只读根文件系统、最小 capability 与显式 CPU/内存/进程/日志上限。Windows amd64 使用同一嵌入前端二进制并由原生 SCM 托管，服务停止映射到既有优雅关闭上下文。
- 生产 secret 由配置 owner 支持显式 `*_FILE` 输入并拒绝同一事实同时来自值和文件；Linux 通过 Compose secrets 只读挂载，Windows 通过仅服务账户可读的本机文件传入。日志、健康检查、进程参数和镜像层不包含 secret。
- migration 是不随 Gateway 启动隐式执行的独立前置 job。升级脚本先验证固定镜像、可用空间、配置与当前 migration 状态，再生成可校验备份并执行前向 migration；两个 Gateway 逐个替换且每个必须 readiness 通过后才继续。
- 应用替换失败时保留健康实例并回退失败实例；已经执行不兼容数据库 migration 时禁止只降应用镜像，必须把升级前备份恢复到新数据库并原子切换 DSN。首次安装、宿主重启、自启动、滚动替换、失败 migration、应用回退、恢复库切换、TLS 长流和结构化日志均由同一隔离部署旅程验证。

### 可观测与支持

- HTTP 指标之外增加 admission 等待/拒绝、协调租约、Provider attempt/错误/冷却、quota 预留/结算、recovery 和后台 Responses 指标。
- 指标标签只使用稳定低基数字段，不包含用户、Key、模型输入或完整上游 URL。
- 提供最小运行看板与阈值规则，并为每个阈值链接可执行 runbook。
- 建立管理员账号恢复、成员密码重置、会话批量撤销和 Gateway Key 无中断更换流程。
- Runtime metrics 是唯一指标 owner：admission/accounting 使用装饰器，requestflow/background 使用只表达终态的 observer；标签只允许封闭的 outcome、operation、Provider kind、canonical error kind，禁止 user/key/model/upstream URL。Caddy 不公开 `/metrics`，监控从 backend 网络直连两个 Gateway。
- 在线成员恢复由管理员+CSRF+幂等 key 发起，事务内更新 Argon2id password hash、撤销该成员全部 session 并写 audit；批量会话撤销同样事务化且允许管理员撤销自己的其他会话。唯一管理员锁定通过 `dbtool` 的显式离线命令、password file 和确认开关恢复，不能经匿名公网找回。
- Gateway Key replacement 从一条 active Key 原子创建同 owner、同模型范围的新 Key并只展示一次 secret，旧 Key保持 active 形成切换窗口；确认客户端已使用新 Key后再走既有幂等撤销。replacement 结果支持相同 idempotency key 重放，不把完整 secret写入数据库、日志或浏览器持久状态。

### 供应链与发布

- Go、Node 和容器依赖执行锁文件完整性、已知漏洞、许可证与维护状态检查。
- Git 历史和构建目录执行 secret 扫描；发布前验证生成物不含 `.env`、备份、日志、截图和测试 secret。
- 构建 Windows amd64 与正式部署目标制品，生成 SBOM、SHA-256、签名/provenance、版本信息和变更说明。
- CI 以相同脚本执行完整验证、race、供应链门槛和制品重建一致性。

### 备份与灾备

- Linux 主机每 6 小时把 PostgreSQL custom dump 与恢复所需的 `/etc/llmgateway` 配置/secret 复制到仅 root 可读 staging，再由固定 Restic 镜像在离开 staging 前写入加密远端仓库；备份成功后执行 7 daily、5 weekly、12 monthly retention 和仓库 check，最后无论成功失败都清理明文 staging。Restic repository、密码和远端凭据分别使用 file source；初始化仓库是独立确认命令，不混入定时任务。
- RPO 目标 6 小时、RTO 目标 2 小时。恢复只写入空目录和新数据库，顺序固定为验证快照与配置 -> 恢复 PostgreSQL/主密钥/pepper -> 启动 PostgreSQL -> 独立 migration/status -> 启动 Valkey/Gateway/Caddy -> 管理员/成员主旅程 -> DNS/TLS 切换；回切仍使用新库与逐实例 readiness，不覆盖原库。
- 灾备演练从空环境恢复数据库、密钥、Gateway 与 Valkey，再执行管理员和成员主旅程；本地隔离 Restic repository 只证明加密/保留/恢复机制，owner 未提供的真实异地 backend 必须保留为准确外部依赖。
- 演练证据不包含 secret、正文或个人数据。

### 成本与经营事实

- Provider 模型价格使用只增不改的生效版本；当前文本范围只含 input/output token 单价，wire 使用十进制定点字符串，领域与 PostgreSQL 使用 ISO 货币代码和整数 currency nanos/百万 Token，不使用浮点数。
- 管理员通过独立幂等价格命令创建版本。请求接受事务必须选择当时已生效版本并把版本、货币和两项费率快照写入 request；缺少价格时在 Provider 发送前明确 fail closed。usage 结算在同一事务内按向上取整到 1 currency nano 计算 input/output/total cost，重放必须匹配原结果，历史不随后来改价变化。
- 管理端提供仅管理员可读、按用户、entitlement/plan、模型、Provider、资源域和货币聚合的用量/成本出口；成员仍只能读取自己的 token usage，不能读取公司采购价。
- 成本事实不参与未知余额伪造，也不改变免费/付费资源域隔离。

## 生产级切片

### 切片 1：真实 Provider 与标准 SDK

- [x] 重建凭据模型绑定控制面，真实管理员可配置并发布每模型优先级与权重，且编辑、重试、刷新对账不会丢失路由事实。
- [x] 固定并记录四家真实供应商、四个 adapter kind 的官方合同、SDK 版本、模型与能力矩阵。
- [x] 建立外部 secret 注入的 canary 工具和脱敏证据格式。
- [x] 完成 models/chat/responses/stream/tools/reasoning/usage/error/cancel 现场验收。
- [x] 完成 Go/Python 标准 SDK 兼容旅程与 Provider 变更复验命令。

### 切片 2：200～300 用户容量与稳态

- [x] 定义可审计流量模型和成功门槛。
- [x] 实现隔离 Provider 负载、长流、突发、429/5xx、断流、强杀和多实例场景。
- [x] 完成并发、连接池、数据库、Valkey、内存和队列调优。
- [x] 运行持续稳态测试并形成容量基线、扩容信号和剩余风险。

### 切片 3：正式部署与升级回滚

- [x] 交付正式拓扑、固定镜像/二进制、TLS 入口和 secret 输入方式。
- [x] 交付安装、自启动、配置校验、migration 前置、日志和健康检查。
- [x] 演练升级前备份、滚动替换、失败回滚、主机重启和恢复库切换。
- [x] 在 staging 完成真实管理员/成员主旅程并形成 production checklist。

### 切片 4：运行观测与账号恢复

- [x] 增加低基数领域指标与稳定日志事件。
- [x] 提供运行看板、阈值规则和对应 runbook。
- [x] 实现管理员驱动成员密码恢复、管理员锁定恢复和会话批量撤销。
- [x] 实现 Gateway Key 重叠更换与旧 Key 撤销的无中断旅程。

### 切片 5：安全供应链与发布物

- [x] 建立 Go/Node/镜像漏洞、许可证和 secret 扫描。
- [x] 生成并校验 SBOM、校验和、版本信息和签名/provenance。
- [ ] 让 Linux/Windows CI 执行完整门槛与 race，并验证发布制品可重建。
- [x] 产出正式发布说明、升级说明和已验证平台清单。

### 切片 6：备份、灾备与经营事实

- [ ] 实现加密、异地、定时备份和保留策略。
- [ ] 定义并演练 RPO/RTO、空环境整站恢复、切换与回切。
- [x] 实现 Provider 价格版本、请求成本快照和聚合对账出口。
- [ ] 用真实合同样例完成额度、usage、成本和毛利核对旅程。

### 切片 7：最终生产验收

- [ ] 连接 staging/production 目标、真实 Provider、真实 PostgreSQL/Valkey 和生产前端。
- [ ] 用有头 Chromium 与标准 SDK 覆盖管理员、成员、取消、刷新、重登、强杀、升级和灾备恢复。
- [ ] 检查网络、DOM、控制台、指标、日志、持久状态、备份和发布制品。
- [ ] 运行唯一完整验证、容量稳态、安全供应链和发布验收，更新全部事实文档。

## 实施任务

- [ ] 每个切片先研究参考项目与官方合同，再核验本仓库 owner 和消费者。
- [ ] 为真实失败建立最少且最有证明力的主旅程或 owner 不变量。
- [ ] 同步 schema、API、adapter、UI、脚本、配置、测试和事实文档。
- [ ] 每完成一个切片立即更新本计划的事实、证据和风险。
- [ ] 最终只按实际部署、运行、恢复和发布证据标记完成。

## 恶劣路径矩阵

| 边界 | 目标结果 | 计划证据 |
| --- | --- | --- |
| Provider 合同变化 | capability/adapter 明确失败并可快速复验 | 真实 canary + SDK |
| 300 用户突发与长流 | 公平、限流、队列和资源上限保持稳定 | load + soak |
| PostgreSQL/Valkey 延迟与短暂中断 | fail closed、无超扣、恢复可解释 | fault load |
| Gateway 强杀与主机重启 | 自启动、fencing、租约和恢复 worker 收敛 | deployment drill |
| 升级 migration 失败 | 数据不损坏，旧版本可恢复服务 | upgrade rollback |
| 唯一管理员锁定 | 受控恢复并撤销旧会话 | account recovery |
| 依赖或镜像漏洞 | 发布门槛阻止不合格制品 | CI security gate |
| 主机/卷丢失 | 在 RPO/RTO 内从异地备份恢复主旅程 | DR drill |
| Provider 改价 | 历史成本不漂移，新请求使用新版本 | cost ledger |
| 发布物篡改 | 校验和、签名/provenance 验证失败 | release verification |

## 验证计划

### 当前基线

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1
```

### 下一阶段新增门槛

- 真实 Provider canary 与 Go/Python SDK 旅程。
- 200～300 用户负载、长流与持续稳态测试。
- 正式部署、升级、回滚、主机重启和恢复库切换。
- `go test -race ./internal/...` 与 Linux/Windows CI。
- Go/Node/镜像漏洞、许可证、secret、SBOM 和制品签名检查。
- 加密异地备份、空环境整站恢复和 RPO/RTO 演练。
- 价格版本、成本快照与合同对账验收。

## 收口

### 当前完成基线

- 双核心、最低生产地基、Provider 扩展入口和本机完整验证已提交为 `d4b3e2c`。
- 2026-07-21 已用隔离 PostgreSQL、Valkey、Provider fixture、真实 Go、生产前端和有头 Chromium 跑通凭据路由绑定创建、丢失响应对账、编辑与发布；`go test ./...`、Web `verify`、`scripts/test-core.ps1` 和 `scripts/test-browser-real.ps1` 均通过。

### 下一阶段完成标准

- 本计划所有切片均有真实环境证据并标记完成。
- staging 与正式部署目标通过管理员、成员、标准 SDK、容量、升级、灾备和发布验收。
- 文档、运行手册、发布物和实现表达同一生产事实。

### 外部依赖

- owner 已提供 Agnes 与智谱各三条、Gemini 一条及硅基流动测试凭据并授权最小真实调用；硅基流动首批 11 条当前为 403，新提供的一条已通过文本 canary。secret 仅通过运行时输入使用，不进入仓库、日志和验收证据。
- 正式域名、TLS、异地备份位置、签名身份和生产主机属于部署时必须确认的外部事实。

### 外部操作

- owner 已授权整体计划完成后检查敏感信息、提交并推送全部生产闭环改动；当前实现阶段不提前提交切片。
