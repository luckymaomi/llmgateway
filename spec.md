# LLMGateway Spec

`spec.md` 是 LLMGateway 产品、架构、边界与设计方向的唯一事实主干。

## 产品定位

LLMGateway 是面向多 Provider 的 LLM 聚合网关。它对外提供统一 API，并集中管理上游账号、模型、密钥、额度、限流、调度、故障切换和用量记录，优先接入合法可用的免费模型、免费套餐与免费额度。

“免费”指 LLMGateway 优先收集和接入用户可合法使用的免费模型、免费套餐或免费额度，不代表任何上游永久免费、无限额度或稳定不变。

## 核心体验

1. 管理员接入多个 Provider 及其独立上游凭据，配置模型、分组、优先级和限制。
2. LLMGateway 为调用方签发统一的网关 API Key。
3. 调用方使用一个 Base URL 和一个 LLMGateway Key 访问已授权模型。
4. 网关完成鉴权、协议归一、模型路由、账号选择、限流、重试和故障切换。
5. 管理端统一查看 Provider、账号池、模型、Key、用量、限流、错误和健康状态。

## 总体架构

```text
Client / SDK / Agent
        |
        v
Public LLM API -> 鉴权与请求归一 -> 路由与账号调度 -> Provider Adapter -> Upstream LLM
        ^                                                               |
        |                                                               v
        +------------ 标准响应 / 流事件 / 错误 / Usage -----------------+
```

- Public API 拥有客户端可见协议、鉴权、校验、流式生命周期和标准错误格式。
- Canonical Model 统一表达请求、响应、流事件、工具调用、推理内容、usage 和错误；无法无损转换的能力必须显式暴露。
- Provider Adapter 只处理对应厂商的鉴权、端点、协议转换、流解析、能力声明和错误归类；OpenAI-compatible Provider 复用通用 Adapter。
- Credential Pool 拥有上游账号与凭据的加密存储、模型授权、禁用、冷却和健康状态。
- Scheduler 与 Resilience Policy 统一拥有账号选择、并发、限流、超时、退避、重试、熔断、切换和恢复规则。
- Usage and Quota 区分上游权威 usage、本地计量和估算值；无法查询时明确标记未知，不伪造余额。
- Control Plane 管理 Provider、账号、模型、分组、LLMGateway Key、策略、用量、日志和健康状态，并向请求数据面发布已生效配置。
- Observability 只记录脱敏请求 ID、路由结果、延迟、usage、限流、重试、熔断和错误类别。

## 产品能力方向

- OpenAI-compatible 的模型列表、Chat Completions、Responses 与流式接口。
- 统一 Function Calling、reasoning content、usage 和错误响应。
- Provider adapter 体系，容纳标准兼容接口和必要的厂商私有协议。
- 多账号与多 API Key 管理，支持模型分组、路由范围和独立策略。
- 全局、用户、Key、Provider 账号和模型维度的并发与速率限制。
- 基于健康、额度、RPM、并发、优先级与粘性需求的调度和故障切换。
- 有边界的重试、退避、熔断、恢复和长流式连接管理。
- 用量、请求、错误、限流与账号健康的脱敏记录和管理界面。
- Windows、Linux、macOS 以及常见 x64/ARM 环境的部署交付。
- 图片、视频和其他多模态接口在对应 Provider 合同明确后纳入统一网关表面。

## Provider 方向

首批候选包括智谱、DeepSeek、Agnes，以及其他提供免费模型或免费额度的 Provider。每个 Provider 的模型、鉴权、额度查询、RPM、流式、工具调用和错误合同必须在实现接入时依据官方资料验证。

Provider 候选不是已支持清单。无法可靠查询余额的账号不能伪造“剩余额度”，只能基于已知限额、实际 usage、响应状态和冷却时间维护可解释状态。

## 边界

- LLMGateway 管理网关调用方与上游账号，但不提供批量注册、接码、养号、绕过风控、规避平台条款或突破免费额度的能力。
- 使用者负责合法取得上游凭据并遵守 Provider 的服务条款、额度和地区要求。
- 上游请求是否可重试由操作语义和发送事实决定；不能仅因切换账号就盲目重放未知副作用。
- 部署模式、用户体系、计费能力和首个发布范围在正式设计阶段确定，当前不由初始化文档预设。

## 数据与安全

- 上游凭据、LLMGateway Key、账户状态和请求内容按敏感数据处理。
- 密钥不得进入 Git、普通日志、错误详情、公开 Issue 或默认遥测。
- 日志和指标默认脱敏，并区分请求事实、路由决策、usage、限流和错误分类。
- 凭据加密、权限模型、轮换、审计和数据保留周期必须在数据模型确定时成为显式合同。

## 授权

LLMGateway 采用 MIT License，作为公开 GitHub 项目协作和分发。

## 正式实现前必须确认

- 技术栈、最低运行环境与构建发布方式。
- 第一阶段协议表面、Provider 范围和管理端范围。
- Provider/账号/模型/Key/策略/usage 的数据模型。
- 调度公平性、粘性会话、限流、重试、熔断和恢复语义。
- 网关鉴权、管理员权限、密钥加密、日志脱敏和数据保留规则。
