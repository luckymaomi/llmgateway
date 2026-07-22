# 控制台信息架构样稿

这是独立于正式 `web/` 的交互样稿，只用于确认控制台功能组织，不连接真实 API，也不进入生产构建。

- 仪表盘：投影 overview、当前额度、请求和 Key 状态。
- 订阅管理：投影现有 entitlement/plan 事实，不包含在线购买或续费。
- 余额记录：投影额度发放、预留、结算、释放和补偿账本。
- Key 管理：投影 Gateway Key，不是 Provider API Key。
- API 日志：投影请求状态、Token、耗时、错误类别和 Request ID。

- `administrator.html`：管理员控制台样稿。
- `index.html`：成员控制台样稿。

管理员样稿借鉴 Sub2API 的账号健康与就地测试、New API 的模型运营分析、LiteLLM 的请求日志筛选，但只投影 LLMGateway 已有事实。OAuth 账号池、粘性会话、支付充值和 iframe 外部系统尚不属于当前产品能力，不在样稿中伪造入口。

本目录长期保留为可打开的交互参考，但不连接真实 API、不进入生产构建，也不成为第二套业务事实或正式前端兼容层。
