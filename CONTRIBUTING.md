# Contributing to LLMGateway

## 开始前

- 阅读 `AGENTS.md`、`spec.md`、`dev.md`，以及存在时的当前 `plan.md`。
- 修改实现、接口、数据、测试、依赖、构建或事实文档时，读取 `.agents/skills/llmgateway-dev/SKILL.md`。
- 中大型正式实现从 `plan.example.md` 创建根目录 `plan.md`；讨论和骨架阶段不创建计划。
- 产品边界、公共协议、安全模型或技术栈变化先在 Issue 或 Discussion 对齐。
- 不提交真实 API Key、私有数据、日志、生成制品或无关改动。

## 变更要求

- 每个变更只服务一个当前目标，但必须闭环所有受影响的 owner、消费者、状态、错误、恢复、测试和文档。
- 阶段范围可以小，进入主干的范围必须达到 `dev.md` 定义的生产级标准。
- Provider 差异进入对应 adapter 或明确 policy，不散落到协议入口、调用方和调度器。
- 不增加旧别名、兼容转发、历史包装、双 schema 读写或临时过渡文件。
- 测试保护真实产品行为，不依赖机器速度、固定睡眠窗口或重复运行。
- 复用参考项目代码前确认许可证、归属和当前版本；Pull Request 说明重要第三方来源与修改。

## 验证

项目骨架变更至少运行：

```powershell
git diff --check
git status --short
```

技术栈确定后，以仓库声明的唯一完整验证命令为准。Pull Request 必须列出实际运行的命令、未验证项和剩余风险，不能用复跑掩盖不稳定测试。

## 安全问题

不要在公开 Issue 中披露漏洞、密钥、请求内容或个人数据。请遵循 `SECURITY.md`。
