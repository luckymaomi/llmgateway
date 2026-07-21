# LLMGateway 双核心生产收口计划

## 需求文档

LLMGateway 面向约 200～300 名受控用户，交付两个核心和最低生产地基：管理员运营多 Provider、模型、凭据与发布目录，成员使用一个 Gateway Key 调用统一 LLM API；管理员同时运营邀请、审核、用户状态、模型权限、额度、RPM、TPM、并发和 Key，成员只管理自己的 Key 与用量。

Provider 是持续扩展面。新增接入必须复用统一公共协议、canonical、调度、账本和恢复边界，通过唯一 Provider catalog 进入请求、探测、校验和管理端。

## 当前事实

- `administrator/member` 身份、邀请审核、额度、Gateway Key 与所有权边界已经接线。
- PostgreSQL baseline、不可变 catalog revision、active version、原子额度账本、请求/attempt/usage/审计和恢复状态已经接线。
- Valkey 承担跨实例 RPM/TPM、并发和短期租约；本地 admission 保证每用户 FIFO、用户轮转和用户并发上限。
- 路由执行硬资格过滤、最小管理员 priority 和同级正 weight；冷却、熔断和有限重试消费稳定错误类别。
- Models、Chat Completions、Responses、流式、工具、reasoning、usage 和 Playground 已接线。
- 凭据使用版本化 AEAD；Gateway Key、邀请、会话与 CSRF 使用摘要；日志脱敏、SSRF 和资源上限已接线。
- Provider catalog 集中拥有 kind、展示名称和 adapter builder，请求执行、凭据探测、Provider 校验和管理端类型列表消费同一事实。
- 已提供显式凭据主密钥轮换、PostgreSQL custom-format 备份、恢复到新数据库和隔离运维演练。
- Windows amd64 嵌入式生产二进制连接 PostgreSQL 18、Valkey 9 与恢复数据库后已通过启动、readiness 和前端交付验证。

## 失败证据

当前没有已知核心产品失败。最新 Provider catalog、运维闭环和全部消费者已经通过唯一完整验证。

## 最终目标

- 管理员能从真实浏览器完成 Provider、模型、凭据、探测、发布、邀请、审核、授权、额度和 Key 全旅程。
- 成员能用一个 Key 稳定调用已授权 Models、Chat Completions 和 Responses，并使用流式、工具、reasoning 与 usage。
- 排队、限流、错误、取消、断连、刷新、重登、强杀、重启、多实例和协调故障都产生可理解且可恢复的结果。
- Provider 扩展通过单一 catalog 与 Adapter 合同闭环，不让厂商差异扩散到调度、账本和公共协议。
- 备份恢复、主密钥轮换和 Windows amd64 生产运行具有可复现命令与隔离演练证据。
- 代码、schema、API、UI、测试和事实文档表达同一当前产品。

## 不做范围

本计划只完成双核心、Provider 扩展合同和最低生产地基，不扩张到没有已确认用户需求的第三条业务主线。

## 设计

```text
Administrator -> Provider / Model / Credential -> Catalog Revision -> Publish
Member -> Gateway Key -> /v1 -> Auth / Quota / Admission
       -> Eligibility -> Priority / Weight -> Provider Adapter
       -> Response / Stream -> Usage Settlement / Recovery
```

| 事实 | Owner |
| --- | --- |
| 用户、角色、邀请、会话、Gateway Key、模型授权 | Identity |
| Provider、模型、凭据、绑定、探测、健康与冷却 | Registry |
| Provider kind、展示名称和 adapter builder | Providers Catalog |
| 已发布目录 | Configuration + PostgreSQL active revision |
| 公共协议与统一语义 | Public API + Protocol + Canonical |
| 公平与短期容量 | Admission + Coordination |
| 候选选择 | Routing |
| 额度与 usage | Quota + PostgreSQL Ledger |
| 执行、发送、流式与恢复 | Requestflow + Execution + Responses |
| 密钥、网络与日志安全 | Security |

配置发布、额度接受和执行状态使用 PostgreSQL 原子边界。Valkey 事实带 TTL 并可重建。未知上游副作用保持 `uncertain`，流提交后不拼接第二个响应，恢复、取消与清理保持幂等。

## 生产级切片

### 双核心产品链

- [x] 管理员与成员身份、权限、额度、Key 和用量闭环。
- [x] 多 Provider 目录、发布、统一 API、调度、限流、usage 和恢复闭环。

### Provider 扩展

- [x] 唯一 Provider catalog、统一 Adapter builder 和动态管理端 kind 列表。
- [x] 请求、探测、写入校验与管理端消费同一 catalog。
- [x] 产品地图、架构和桌面提示词记录扩展准入与验证步骤。

### 最低生产地基

- [x] 凭据加密、Key 摘要、会话、CSRF、权限、脱敏、SSRF 和资源上限。
- [x] PostgreSQL 原子账本与配置发布、Valkey 短期协调、取消/断连/强杀恢复。
- [x] 原子主密钥轮换、PostgreSQL 备份恢复和 Windows amd64 生产运行演练。

### 最终验证

- [x] 全量 Go 测试与生产前端验证。
- [x] migration round-trip、真实控制面和真实核心网关链。
- [x] 有头 Chromium 桌面与移动真实主旅程。
- [x] 运维与生产运行隔离演练。
- [x] 运行最新代码的唯一完整验证入口并检查最终差异。

## 实施任务

- [x] 研究参考项目固定版本、许可证、adapter、路由、限流和恢复机制。
- [x] 核验本项目客户端到 usage、管理写入到数据面发布的 owner 与消费者。
- [x] 完成 schema、sqlc、Go、Web、脚本和事实文档同步。
- [x] 运行定向测试并修复真实失败。
- [x] 运行真实 PostgreSQL、Valkey、隔离 Provider 和有头 Chromium 旅程。
- [x] 演练主密钥轮换、备份恢复和 Windows amd64 生产运行。
- [x] 运行 `scripts/verify.ps1`，记录最终通过项与剩余外部风险。

## 恶劣路径矩阵

| 边界 | 稳定结果 | 证据 |
| --- | --- | --- |
| 未授权模型、停用用户、撤销 Key | 发送前拒绝 | core/browser |
| 单用户大量请求 | 每用户 FIFO、用户轮转、用户并发上限 | admission/core |
| 两实例共享容量 | 不突破全局与用户容量 | core |
| Valkey 中断 | fail closed，已发送请求取消并保留真实状态 | core |
| 并发额度竞争 | 原子预留与一次结算 | quota/store/core |
| 429、5xx、超时与冷却 | 稳定错误、持久冷却、有限安全重试 | Provider fixture/core |
| 客户端取消、断连与 partial stream | 传播取消并按发送事实结算或保持 uncertain | core/browser |
| 进程强杀与重启 | fencing、租约过期和持久恢复 | core/browser |
| 配置并发发布与回滚 | 唯一 active version | control/core |
| 主密钥轮换 | 全部旧凭据同事务重加密，重复执行无新增变更 | operations |
| PostgreSQL 恢复 | custom archive 恢复到新库并保留凭据、审计与 schema version | operations |
| secret 泄漏 | 数据库摘要/密文与运行日志不出现完整 secret | core/browser |

## 验证计划

唯一完整入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1
```

它覆盖环境、格式、vet、Go、sqlc 漂移、前端、mock 有头浏览器、真实有头浏览器、Compose、migration、控制面、核心链、运维演练和构建矩阵。

## 收口

### 已通过

- `go test ./...`
- `pnpm.cmd --dir web run verify`
- `scripts/test-migrations.ps1`
- `scripts/test-control.ps1`
- `scripts/test-core.ps1`
- `scripts/test-browser-real.ps1`
- `scripts/test-operations.ps1`
- `scripts/verify.ps1`

### 外部风险

- 本轮隔离 Provider 验收不使用真实 GLM/Agnes 凭据；正式接入具体上游时仍需按当时官方合同与真实 wire 复核模型、能力、限额和错误。

### 外部操作

- 保留全部 owner 未提交改动；不 commit、不 push、不发布、不部署。
