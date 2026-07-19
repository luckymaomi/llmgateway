# BirdAPI Architecture

本文档定义 BirdAPI 的长期架构方向。技术栈确定后，目录和类型可以变化，但职责边界与数据流必须保持清楚。

## 系统视图

```text
Client / SDK / Agent
        |
        v
Public LLM API
        |
        v
Authentication -> Request normalization -> Routing policy
                                           |
                                           v
                                  Account scheduler
                                           |
                                           v
                                  Provider adapter
                                           |
                                           v
                                     Upstream LLM
                                           |
                                           v
Response normalization -> Streaming / Error / Usage -> Client
                                           |
                                           v
                                  Audit and observability
```

管理面负责 Provider、账号、模型、分组、BirdAPI Key、策略、用量和运行状态；数据面只消费已经生效的配置和策略，不在请求路径临时发明规则。

## 核心边界

### Public API

拥有客户端可见协议、鉴权入口、请求校验、流式生命周期和标准错误格式。不包含厂商私有判断。

### Canonical Model

定义网关内部可表达的请求、响应、流事件、工具调用、推理内容、usage 和错误事实。Provider 无法无损表达的能力必须显式标记，不能静默丢弃。

### Provider Adapter

每个 adapter 只负责一个 Provider 的鉴权、端点、协议转换、流式解析、能力声明和错误归类。标准 OpenAI-compatible Provider 复用通用 adapter；真实差异进入明确 policy。

### Credential Pool

拥有上游账号与凭据的生命周期、加密存储、模型授权、人工禁用、冷却和健康状态。真实密钥不离开凭据边界，不进入普通日志。

### Scheduler

根据模型能力、账号状态、优先级、并发、RPM、Token 限制、额度事实和粘性需求选择账号。调度结果必须可解释，并避免一个繁忙账号阻塞整个池。

### Resilience Policy

拥有超时、退避、重试、熔断、恢复和账号切换规则。只有确定可安全重放的请求才允许自动切换后重试；流已经对客户端输出后必须按流式合同收口错误。

### Usage and Quota

区分 Provider 返回的权威 usage、BirdAPI 本地计量和估算值。额度查询不可用时暴露“未知”，不伪造余额。

### Control Plane

提供管理员 API 与管理界面，维护 Provider、账号、模型、分组、Key、策略、用量、日志和健康状态。所有配置变更形成可审计事实，再发布给数据面。

### Observability

记录脱敏请求 ID、路由结果、延迟、usage、限流、重试、熔断和错误类别。日志不能成为调度状态的第二事实源。

## 关键合同

- Provider 能力由结构化 capability 描述，不由模型名称猜测。
- 模型别名和路由映射是显式配置，未知模型不静默回退。
- 限流至少区分全局、调用方 Key、上游账号和模型维度。
- 健康状态必须有来源、时间和恢复条件；临时错误不永久封禁账号。
- 请求记录、usage、调度状态和凭据各有唯一 owner。
- 管理面与数据面共享持久事实，不复制不可对账的内存状态。

## 跨平台方向

- 构建与发布覆盖 Windows、Linux、macOS。
- 运行时避免依赖只在单一平台存在的 shell、进程管理或文件权限假设。
- x64 与 ARM 支持以真实 CI/build 结果为准，不从语言可交叉编译能力直接推断产品已支持。

## 待正式设计

- 技术栈和仓库目录。
- 管理面与数据面是单进程还是可拆分部署。
- 持久化、加密和迁移策略。
- 首期 OpenAI-compatible 接口集合。
- 首批 Provider 与免费额度事实获取方式。
- 用户、权限、计费和部署模式。
