# LLMGateway

LLMGateway 是面向多 Provider 的 LLM 聚合网关：对外提供统一接口，并集中管理账号、模型、Key、额度、限流、调度、故障切换和用量。项目优先接入合法可用的免费模型、免费套餐与免费额度。

## 方向

- 一个 Base URL、一个 LLMGateway Key 调用多个上游模型。
- OpenAI-compatible 模型列表、Chat Completions、Responses 与流式接口。
- 统一工具调用、推理内容、usage 和错误格式。
- 多 Provider、多账号池、模型分组与 Key 分发。
- 并发控制、RPM/Token 限流、健康调度、有界重试、熔断与恢复。
- 脱敏请求日志、用量统计、账号健康和管理界面。
- Windows、Linux、macOS 与常见 x64/ARM 部署交付。

“免费”描述优先聚合合法可用的免费模型、免费套餐与免费额度，不构成任何上游永久免费或无限额度承诺。

## 项目文档

- [`spec.md`](spec.md)：产品定位、能力方向与边界。
- [`AGENTS.md`](AGENTS.md)：长期开发和安全规则。
- [`dev.md`](dev.md)：生产级阶段、事实 owner 与恶劣路径验收纪律。
- [`plan.example.md`](plan.example.md)：正式实现任务的计划模板。
- [`CONTRIBUTING.md`](CONTRIBUTING.md)：贡献要求。
- [`SECURITY.md`](SECURITY.md)：安全报告方式。

## 开发约定

正式实现前先对齐技术栈、首期协议表面、Provider 范围、数据模型和安全合同。中大型实现任务从 `plan.example.md` 创建根目录 `plan.md`，任务收口后由下一项实际任务替换。

真实 API Key、`.env`、日志和敏感请求数据不得提交到仓库。

## License

[MIT](LICENSE)
