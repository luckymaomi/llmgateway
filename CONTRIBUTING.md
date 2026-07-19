# Contributing to BirdAPI

## 开始前

- 阅读 `AGENTS.md`、`spec.md`、`docs/architecture.md`，以及存在时的当前 `plan.md`。
- 产品边界、公共协议、安全模型或技术栈变化先通过 Issue 或 Discussion 对齐。
- 正式实现阶段的中大型任务先读取 `.agents/skills/plan/SKILL.md` 与 `.agents/skills/birdapi-dev/SKILL.md`，并从 `plan.example.md` 创建当前 `plan.md`。
- 不提交真实 API Key、私有数据、日志、生成制品或无关改动。

## 变更要求

- 每个变更只服务一个当前目标。
- 实现、测试、配置、README 与 `spec.md` 描述同一套事实。
- Provider 差异进入对应 adapter 或明确 policy，不散落到协议入口和调用方。
- 不增加旧别名、兼容转发、历史包装或临时过渡文件。
- 测试必须覆盖真实产品规则和错误边界，不依赖机器速度或固定睡眠窗口。

## 验证

项目骨架变更至少运行：

```powershell
git diff --check
git status --short
```

技术栈确定后，以仓库届时声明的统一完整验证命令为准。Pull Request 必须列出实际运行的命令、未验证项和剩余风险。

## 安全问题

不要在公开 Issue 中披露漏洞、密钥或个人数据。请遵循 `SECURITY.md`。
