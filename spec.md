# LLMGateway Spec

`spec.md` 是当前产品事实与边界的主干；当前实施状态以根目录 `plan.md` 为准。

## 产品定位

LLMGateway 是面向约 200～300 名受控用户的中小微企业级多 Provider LLM 网关。产品以模块化单体交付，在聚焦的功能范围内保持完整的安全、并发、持久化、恢复和运维边界。

产品只有两个核心：

1. 多 Provider 统一 API：管理员管理 Provider、模型和上游凭据，发布统一目录；成员用一个 Gateway Key 调用已授权模型。
2. 用户与管理员：管理员邀请、审核、停用成员，分配模型权限、额度、RPM、TPM、并发和 Key；成员只管理自己的 Key 并查看自己的用量。

最低生产地基与双核心同等重要，不因缩小功能范围而降低质量。

## 公共 API

- OpenAI-compatible `GET /v1/models`、`POST /v1/chat/completions` 和 `POST /v1/responses`。
- Chat Completions 与 Responses 支持非流式、流式、工具调用、reasoning 和 usage；无法无损表达的能力在发送前明确拒绝。
- Provider adapter 拥有厂商 wire 差异；Canonical Model 拥有统一内部语义；Public API 拥有客户端可见合同。
- 首批专用 adapter 为智谱 GLM 和 Agnes，同时保留明确能力子集的通用 OpenAI-compatible adapter。
- Provider kind、展示名称和 builder 由唯一 catalog 注册；请求、探测、写入校验和管理端类型列表消费同一事实。新增 Provider 优先复用通用兼容合同，只有官方合同与隔离 wire 证明存在无法无损表达的差异时才增加专用 adapter。
- 模型名、能力、错误、RPM/TPM 和额度属于易变上游事实，接入时必须依据官方资料和隔离 wire 核验；未知余额不得伪造成剩余额度。

## Provider 与调度

- Provider、模型、凭据和模型绑定由管理员维护；上游 secret 使用版本化 AEAD 加密保存。
- 发布操作捕获不可变 catalog revision，PostgreSQL 的 active revision 是数据面唯一已发布配置事实。
- 路由先过滤模型、资源域、能力、启停、授权、冷却和本次已尝试凭据；再选择数值最小的管理员优先级，并在同优先级凭据中按正权重选择。
- RPM、TPM 和并发不参与虚构综合评分，由 Valkey 原子短期租约和限流计数拥有；额度由 PostgreSQL 原子账本拥有。
- 免费与付费资源域严格隔离。免费资源不可用时返回可理解错误，不自动消耗付费资源。
- 只在已知安全发送边界内有限重试；上游副作用未知或流已经提交后不换凭据盲目重放。
- 本地 admission 保证每用户 FIFO、用户间轮转和每用户并发上限；多实例使用 Valkey 共享容量，不承诺进程重启后保留绝对排队顺序。

## 身份、权限与额度

- 系统只有 `administrator` 和 `member` 两种角色。
- 第一位管理员通过一次性 setup 建立；之后成员必须持邀请码注册并由管理员审核。
- 管理员可以管理 Provider、模型、凭据、发布配置、用户状态、授权、额度和任意用户的 Gateway Key。
- 成员只能查看自己的授权、额度、用量和 Key，并只能撤销自己的 Key。
- Gateway Key 与邀请码完整值只在创建或同一幂等操作恢复时返回；PostgreSQL 只保存摘要、前缀和不含 secret 的 mutation 结果。
- 配额请求先原子预留，再按上游权威 usage 结算；并发竞争不得超扣、重复结算或漏结算。

## 最低生产地基

- PostgreSQL 是用户、配置、请求、attempt、额度、usage 和审计的持久事实 owner。
- Valkey 只拥有可过期、可重建的限流计数和并发租约；协调不可用时 fail closed。
- 管理会话使用 HttpOnly cookie、独立 CSRF token、过期和撤销；停用用户会阻止后续管理和公共调用。
- 自定义 Provider URL 在发送前经过 SSRF、DNS 重绑定和重定向策略；默认不允许访问内网与回环地址。
- 日志、错误、指标、审计和浏览器持久状态不得保存 Provider secret、Gateway Key、邀请码或请求正文。
- HTTP body、响应字节、队列、并发、连接、流和超时都有显式上限。
- 客户端取消、断连、partial stream、429/5xx、协调失败、进程强杀与重启必须留下可解释终态；未知上游副作用保持 `uncertain`，不自动重放。
- 首次生产交付必须具有可验证的 PostgreSQL 备份恢复、受控主密钥轮换和一个真实部署目标；未实际演练前不得宣称完成。

## 工程与交付不变量

- 当前没有必须保留的正式生产数据；首次发布前的不兼容 schema、API、配置和事件变化直接重建当前基线，不维护双读写或过渡 migration。
- 每个事实只有一个 owner，handler、UI、adapter 和测试不保存第二套业务事实。
- 阶段可以缩小功能范围，进入范围的成功、错误、并发、中断、恢复、安全、可观测性、测试、文档和目标环境验证必须闭环。
- 最终验收连接真实 Go、PostgreSQL、Valkey、生产构建前端和隔离 Provider，并以有头 Chromium 覆盖桌面与移动主旅程。
- LLMGateway 使用 MIT License；参考仓库只研究机制与失败经验，不复制许可证不兼容源码。
