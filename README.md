# LLMGateway

<div align="center">

[![Verify](https://github.com/luckymaomi/llmgateway/actions/workflows/verify.yml/badge.svg)](https://github.com/luckymaomi/llmgateway/actions/workflows/verify.yml)
[![Go](https://img.shields.io/badge/Go-1.26.5-00ADD8.svg)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB.svg)](https://react.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-18-4169E1.svg)](https://www.postgresql.org/)
[![Valkey](https://img.shields.io/badge/Valkey-9-DC382D.svg)](https://valkey.io/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**把多个大模型 Provider 收进一个入口，让团队只使用一个 Base URL 和自己的 Gateway Key。**

面向约 200～300 名受控用户的多 Provider LLM 网关，提供统一 API、成员管理、额度治理、可靠路由和可审计用量。

</div>

## 它解决什么问题

- 应用和 SDK 不再分别适配每个 Provider，统一使用 OpenAI-compatible Models、Chat Completions 和 Responses。
- 上游 API Key 只由管理员集中保管；成员拿到的是权限和额度受控的 Gateway Key。
- Provider、模型、凭据、优先级、权重、免费/付费资源域和发布版本在一个管理端维护。
- 管理员邀请并审核成员，分配模型、Token 额度、RPM、TPM、并发和 Key；成员只看自己的 Key 与用量。
- 请求取消、上游 429/5xx、流中断、进程强杀和短暂基础设施故障都有明确恢复边界。
- PostgreSQL 保存权威事实，Valkey 负责可过期协调，并提供日志、指标、审计、备份和恢复工具。

LLMGateway 本身不处理充值、收款、开票或在线售卖套餐。公司先通过合同或内部审批确定服务，再由管理员在网关中分配访问权和额度。

## Windows 5 分钟启动

这是从源码启动管理网页的最简单方式。正式服务器部署请看 [deploy/README.md](deploy/README.md)。

### 1. 准备环境

请在 Windows 10/11 64 位系统安装并启动以下软件：

| 必需软件 | 最低版本 | 用途 |
| --- | --- | --- |
| [Git for Windows](https://git-scm.com/download/win) | 当前受支持版本 | 读取源码状态 |
| [Python](https://www.python.org/downloads/windows/) | 3.10 | 运行根目录友好命令；安装时勾选加入 PATH |
| [Docker Desktop](https://www.docker.com/products/docker-desktop/) | 含 Docker Compose | 运行 PostgreSQL 与 Valkey；执行前保持 Docker Desktop 已启动 |
| [Go](https://go.dev/dl/) | 1.26.5 | 构建 Gateway |
| [Node.js](https://nodejs.org/) | 22.12 | 构建管理网页 |
| pnpm | 10.33.0 | 安装锁定的网页依赖 |

安装 Node.js 后，在 PowerShell 中安装 pnpm：

```powershell
npm.cmd install --global --ignore-scripts pnpm@10.33.0
```

不确定环境是否齐全时，在仓库根目录运行：

```powershell
python .\start_dev.py --check
```

检查失败会直接指出缺少的软件或版本。所有检查通过后再继续。

### 2. 启动并打开网页

```powershell
python .\start_dev.py
```

首次启动会安装锁文件指定的网页依赖并构建 Gateway，完成后自动打开 `http://127.0.0.1:5173`。这个窗口需要保持运行；按 `Ctrl+C` 会停止网页和 Gateway，但 PostgreSQL、Valkey 及已有数据会保留。

脚本不会读取本地上游 API Key，也不会自动调用任何收费模型。

Windows 开发入口允许出站请求解析到 `198.18.0.0/15`，用于兼容透明代理使用的 Fake-IP。这个放宽只属于本机开发配置；正式部署不继承，生产仍按显式 SSRF 网络策略失败关闭。Provider 模板安装本身只创建停用的 Provider 和模型，不解析 DNS 或发送上游请求；添加上游 API Key 后的就地测试和正式请求仍会执行完整地址校验。

### 3. 第一次真正使用

全新数据库没有默认账号或默认密码。第一次打开网页时按下面顺序操作：

1. 在“初始化系统”页面填写管理员邮箱。服务端会生成首位管理员的高熵初始密码，并只在成功页面显示一次；先存入密码管理器并确认已保存，再进入控制面。该入口只在系统尚未初始化时有效。
2. 进入“Provider”，从 Agnes、智谱 GLM、Google Gemini 或硅基流动模板开始；模板会安装已核验端点和文本模型，自定义 Provider 仍可单独添加。
3. 在“模型”确认网关别名、上游模型 ID、资源域和能力，再到“上游 API Key”添加真实凭据、绑定模型并执行就地测试。上游 API Key 只由管理员维护。
4. 进入“配置发布”，依次捕获当前配置、校验和发布。未发布的模型不会进入数据面。
5. 在“上游成本”新增模型价格，再到“订阅与额度”给实际调用成员分配额度。缺少生效价格或额度时，请求会在发送给 Provider 前被拒绝。
6. 在“邀请”创建一次性邀请码；成员注册后，到“成员”审核并激活账号。
7. 进入“Gateway Key”创建调用 Key，选择所属成员和授权模型。完整 Key 只显示一次；结果页会同时给出当前 Base URL，并可一次复制调用配置。
8. 在同一 Key 行执行真实统一 API 测试；成功后再接入自己的 SDK。日常排障直接进入“运维监控”和“API 日志”。

首位管理员是自助初始化，后续成员不是自由注册：管理员在“邀请”创建一次性邀请码，成员从登录页进入“邀请注册”，提交后还要由管理员在“成员”审核通过。

登录后可从桌面或移动侧栏进入“更换密码”。操作需要当前密码；成功后当前会话继续有效，其他登录会话立即撤销。初始密码、当前密码和新密码都不会写入日志、审计或浏览器持久存储。

### 4. 让 SDK 调用

本地 Base URL 是 `http://127.0.0.1:8080/v1`，API Key 使用上一步生成的 Gateway Key，不是上游 API Key。以 Python OpenAI SDK 为例：

```powershell
python -m pip install openai==2.46.0
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key="这里换成只显示一次的 Gateway Key",
)

response = client.chat.completions.create(
    model="这里换成已发布并授权的模型名",
    messages=[{"role": "user", "content": "你好"}],
)
print(response.choices[0].message.content)
```

不要把任何真实 Key 写进仓库、截图、日志或工单。

## 以后怎么继续、停止和清空

继续上次的数据并重新打开网页：

```powershell
python .\start_dev.py
```

正常停止分两步：先在启动窗口按 `Ctrl+C` 停止网页和 Gateway；不再需要后台 PostgreSQL/Valkey 时再运行：

```powershell
python .\stop_dev.py
```

停止命令保留数据库卷，下次启动会接着使用原账号、Provider、配置、Key 和账本。

想从零开始时运行：

```powershell
python .\reset_dev.py
```

重置会要求输入 `RESET`，只删除当前 LLMGateway Compose 项目的本地 PostgreSQL/Valkey 容器和数据卷，然后重新启动。它不会删除源码、`.env`、Key 文件或其他 Docker 项目。自动化环境可显式使用 `--yes`，只清理不重启可加 `--no-start`。

## Gateway Key 怎么更换

Gateway Key 与上游 API Key 是两件事：前者给成员或应用调用 LLMGateway，后者让 LLMGateway 调用上游。

1. 成员在“Key 管理”、管理员在“Gateway Key”中点击旧 Key 的“更换”。
2. 复制只显示一次的新 Key，并先更新、验证所有客户端。
3. 此时旧 Key 仍可用，避免切换过程中突然中断。
4. 确认客户端全部切换后，再对旧 Key 点击“撤销”；撤销后使用它的请求会立即失败。

上游 API Key 的更换在“上游 API Key”完成。先增加并探测新 Key、调整模型绑定并发布配置，确认流量接管后再停用旧 Key；免费资源域不会自动落到付费凭据。

## 测试

所有人工测试统一从 `start_test.py` 进入。直接运行 `python .\start_test.py` 会显示编号菜单，输入 `1` 到 `6` 选择测试；也可以把档位直接写在命令后。控制台会实时显示输出，同时写入不受 Git 跟踪的 `.build/test-logs/`；成功或失败时最后一行都会给出日志路径。长测试由 owner 在自己的终端运行，完成后把该路径告诉 Agent 即可，Agent 不需要持续轮询进程。

| 命令 | 范围 | 常见耗时 |
| --- | --- | --- |
| `python .\start_test.py daily` | 格式、静态分析、Go、sqlc、前端测试与构建 | 约 2 分钟 |
| `python .\start_test.py full` | daily + 浏览器、Docker 集成、强杀、SCM、TLS 升级、灾备、构建矩阵 | 约 15 分钟 |
| `python .\start_test.py provider` | 真实 Provider、Go/Python SDK、quota 与接管 | 取决于外部网络 |
| `python .\start_test.py capacity` | 300 用户、60 活跃用户、双实例、长流、突发与强杀恢复 | 默认约 2 分钟 |
| `python .\start_test.py release` | 测试签名发布物、OCI、SBOM、checksum、provenance | 约 10～20 分钟 |
| `python .\start_test.py everything` | 发布候选组合验收：依次运行 full、provider、capacity、release，首个失败停止 | 约 30～60 分钟 |

正式 15 分钟容量证据使用：

```powershell
python .\start_test.py everything --capacity-duration-seconds 900
```

`everything` 用于发布候选或计划明确要求的跨门槛总证据，不是普通页面、Provider 或运营迭代的默认完成条件。日常修改先运行最接近 owner 的短测试；真实 Provider、容量、发布物和完整组合按对应风险与频率单独运行。

真实 Provider 模式只检查并在测试进程内使用根目录 `key.txt`，不会把凭据写入测试日志。查看全部参数使用 `python .\start_test.py --help`。底层 `scripts/*.ps1` 由统一入口编排，不作为日常人工命令。

## 常见问题

- `python`、`go`、`node`、`pnpm` 或 `docker` 找不到：安装后关闭并重新打开 PowerShell，再运行 `python .\start_dev.py --check`。
- Docker 报未就绪：打开 Docker Desktop，等待界面显示 Engine 已启动。
- `5173` 或 `8080` 被占用：先停止占用端口的旧开发窗口，或使用 `python .\start_dev.py --web-port 15173 --gateway-port 18080`。
- 忘记管理员密码：成员密码可由管理员在网页重置；唯一管理员锁定属于受控离线恢复，按 [运行手册](deploy/observability/runbook.md) 操作。
- 忘记完整 Gateway Key：系统只保存摘要，无法找回；按上面的重叠更换流程创建新 Key。
- 页面能打开但模型不可用：依次检查凭据探测、模型绑定、配置版本是否发布、价格版本、用户额度和 Key 模型授权。

## 文档地图

- [spec.md](spec.md)：唯一的产品与系统规格，包含产品边界、架构、数据流、容量和部署拓扑。
- [dev.md](dev.md)：唯一的开发与验收标准，重点约束失败、中断、并发、恢复、安全和测试门槛。
- [history.md](history.md)：项目演进、删除路线、真实失败和最终保留实践；不替代当前规格或计划。
- [deploy/README.md](deploy/README.md)：正式安装、升级、回滚、备份和灾备操作。
- [RELEASE.md](RELEASE.md)：当前版本随发布包携带的简明说明，不重复产品规格。
- [CONTRIBUTING.md](CONTRIBUTING.md) 与 [SECURITY.md](SECURITY.md)：贡献要求和安全报告方式。

维护者当前任务和完成证据只写入根目录 `plan.md`，它不是用户规格，也不随发布包交付。`deploy/` 保存正式运行与恢复 owner，`scripts/` 保存开发、验证和发布底层入口；普通使用者只需要本页列出的根目录 Python 命令。

## License

[MIT](LICENSE)
