# LLMGateway

LLMGateway 是面向约 200～300 名受控用户的中小微企业级多 Provider LLM 聚合网关：对外提供统一接口，并集中管理 Provider、模型、凭据、用户、Key、额度、限流、调度、故障切换和用量。

## 方向

- 一个 Base URL、一个 LLMGateway Key 调用多个上游模型。
- OpenAI-compatible 模型列表、Chat Completions、Responses 与流式接口。
- 统一工具调用、推理内容、usage 和错误格式。
- 多 Provider、多凭据池、已发布模型目录与 Key 分发。
- 并发控制、RPM/Token 限流、健康调度、有界重试、熔断与恢复。
- 脱敏请求事实、用量统计、凭据健康和管理员/成员界面。
- PostgreSQL 持久事实、Valkey 短期协调、取消与强杀恢复。

“免费”描述优先聚合合法可用的免费模型、免费套餐与免费额度，不构成任何上游永久免费或无限额度承诺。

## 项目文档

- [`spec.md`](spec.md)：产品定位、能力方向与边界。
- [`architecture.md`](architecture.md)：技术栈、模块边界、运行拓扑与管理端信息架构。
- [`AGENTS.md`](AGENTS.md)：长期开发和安全规则。
- [`dev.md`](dev.md)：生产级阶段、事实 owner 与恶劣路径验收纪律。
- [`plan.example.md`](plan.example.md)：正式实现任务的计划模板。
- [`CONTRIBUTING.md`](CONTRIBUTING.md)：贡献要求。
- [`SECURITY.md`](SECURITY.md)：安全报告方式。

## 开发约定

当前产品合同以 `spec.md` 为准，中大型实现任务以根目录 `plan.md` 为唯一执行合同。

真实 API Key、`.env`、日志和敏感请求数据不得提交到仓库。

## 本地一键启动

前置条件：Docker Desktop、Node.js 22.12+、pnpm，以及 Go 1.26.5 或更新的 1.26 补丁版。

`scripts/dev.ps1` 仅支持 Windows PowerShell。Windows 在仓库根目录执行一条命令即可启动 PostgreSQL、Valkey、真实 Go gateway 和 Web 开发服务器：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\dev.ps1
```

启动完成后终端会输出 Web UI、Gateway 和 readiness 地址。需要自动打开默认浏览器时使用：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\dev.ps1 -OpenBrowser
```

默认 Web 端口为 `5173`、Gateway 端口为 `8080`；端口被其他程序占用时可以显式覆盖：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\dev.ps1 -WebPort 15173 -GatewayPort 18080 -OpenBrowser
```

脚本只接受 `compose.yaml` 所属且仅绑定回环地址的 PostgreSQL/Valkey，从实际容器读取连接事实，并让 Vite 同源代理指向本轮 Gateway。首次缺少 Web 依赖时会执行锁文件固定的安装。脚本不会读取 Provider Key，也不会发起 Provider 调用。

按 `Ctrl+C` 会停止本轮脚本直接启动的 Gateway、Web 进程并删除本轮 `.build` 目录，不会停止 Compose 容器或删除持久卷。只启动基础设施可使用：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\dev.ps1 -InfrastructureOnly
```

验证当前环境：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-environment.ps1
```

停止基础设施但保留数据：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\stop-dev.ps1
```

Linux 与 macOS 使用同一份 `compose.yaml`：`docker compose up -d` 与 `docker compose down`。默认 PostgreSQL 位于 `127.0.0.1:15432`，Valkey 位于 `127.0.0.1:16380`；本地覆盖值写入不进 Git 的 `.env`。

### Go 最基本的运行方式

Go 通常运行一个 package，而不是指定某个 `.go` 文件。本项目后端入口 package 位于 `cmd/gateway`：

```powershell
# 基础设施已启动且使用默认 Compose 配置时，直接运行后端
go run .\cmd\gateway

# 编译后端；输出路径可以自行指定
go build -o .\.build\gateway.exe .\cmd\gateway

# 运行仓库中的 Go 测试
go test ./...
```

`go.mod` 和 `go.sum` 相当于 Go 的依赖合同，`go run`、`go build` 和 `go test` 会按它们解析依赖。Web 仍是独立的 Node/Vite 进程；手工开发时在另一个终端设置 `VITE_API_PROXY_TARGET=http://127.0.0.1:8080` 后执行 `pnpm.cmd --dir web run dev`。一键脚本已经代管这些环境变量和进程关系。

## 浏览器验收

Playwright 默认以有头模式依次打开桌面 Chrome 与 Pixel 7 视口，方便直接观察真实点击、导航和表单状态：

```powershell
pnpm.cmd --dir web run test:e2e
```

生产二进制先确定性构建前端，再用 `webembed` 标签把 `web/dist` 嵌入 Gateway；运行时不需要 Node 或独立静态服务器：

```powershell
pnpm.cmd --dir web run build
go build -tags webembed -trimpath -o .\.build\llmgateway.exe .\cmd\gateway
```

当前 E2E 使用 mock server 做管理端结构与交互回归；它不能替代连接真实 Go、PostgreSQL、Valkey 和构建前端的最终验收。完整验证入口会调用同一套有头测试；Linux CI 通过 Xvfb 提供显示环境。

Provider 管理的真实后端验收会创建并精确清理本轮专属的 PostgreSQL 与 Valkey 容器，构建带嵌入前端且可重启的真实 gateway，并直接通过该 Go 服务完成桌面与 Pixel 7 点击：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-browser-real.ps1
```

该命令不会读取本地 `.env` 或 Provider Key，也不会触碰 Compose 开发卷。它覆盖管理员初始化、Provider 创建/编辑/冲突/未知结果恢复/启停、配置捕获/发布、刷新与进程强杀重启；邀请路径会在 PostgreSQL 已提交后主动丢弃 HTTP 回执，保留非敏感 pending operation，强杀并重启 gateway，再用同一幂等键恢复同一邀请码。随后真实注册、审核、签发/复制/撤销 Key、直连 `/v1/models`、Playground 流式成功/取消和成员自有 usage 查询，并验证桌面与 Pixel 7 的导航、身份名称、持久状态、secret 清理和页面宽度约束。

## 生产运维

本轮验证的部署目标是 Windows amd64 嵌入式 Go 二进制，连接 PostgreSQL 18 与 Valkey 9 容器；公网 TLS 由受控反向代理终止。生产配置关闭启动时自动 migration，由发布流程先显式执行 `dbtool -action up`。

创建 PostgreSQL custom-format 备份：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\backup-postgres.ps1 -OutputPath D:\llmgateway-backups\llmgateway.dump
```

恢复命令只接受一个尚不存在的新数据库名，便于校验后再由运维切换连接：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-postgres.ps1 -InputPath D:\llmgateway-backups\llmgateway.dump -TargetDatabase llmgateway_restore_20260721 -ConfirmRestore
```

轮换 Provider 凭据主密钥时，先让所有实例同时配置旧版本与新版本并把 active version 指向新密钥，再执行：

```powershell
go run .\cmd\dbtool -action rotate-credentials -confirm-key-rotation
```

命令在一个 PostgreSQL 事务内认证并重加密全部旧版本凭据，写入不含 secret 的审计；幂等重跑完成后再备份和验证，最后从所有实例移除旧密钥。备份文件与主密钥分别保管。

隔离演练入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-operations.ps1
```

## License

[MIT](LICENSE)
