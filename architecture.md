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

## 交付拓扑

当前生产形态是一份包含前端静态资源的 Go 二进制，连接受控 PostgreSQL 18 和 Valkey 9，并由反向代理终止公网 TLS。实际声明支持的操作系统、架构和部署目标只以完成过的构建、启动、主旅程、强杀恢复、备份恢复与轮换演练为准。
