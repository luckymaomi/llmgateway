# LLMGateway 0.1.0 发布说明

本文是当前首次生产发布的交付合同。产品事实以 `spec.md` 为准，部署命令以 `deploy/README.md` 为准，当前完成度和未完成风险以 `plan.md` 为准。在 `plan.md` 的灾备、成本和最终生产验收切片完成前，`0.1.0` 仍是发布候选，不能仅凭构建成功对外宣称已投产。

## 发布范围

- OpenAI-compatible Models、Chat Completions 与 Responses，覆盖非流、SSE、工具调用、reasoning、usage、取消和稳定错误。
- Agnes、智谱 GLM、Google Gemini 专用 adapter，以及经官方 wire 证明的通用 OpenAI-compatible 文本 adapter。
- 管理员/成员身份、邀请审核、模型授权、原子额度账本、Gateway Key、Key 重叠更换和账号恢复。
- PostgreSQL 持久事实、Valkey 短期协调、优先级/权重路由、有界重试、未知副作用保护、强杀与多实例恢复。
- Linux 双 Gateway/Caddy TLS Compose 与 Windows 原生 SCM 服务，独立 migration、滚动替换、每 6 小时加密 Restic 备份和空环境恢复库切换。
- 低基数 Prometheus 指标、Grafana 看板、稳定异常事件、供应链门槛与可验证发布物。
- 不可变 Provider 模型价格版本、请求成本快照和仅管理员可读的采购成本聚合；缺价模型发送前 fail closed。

## 已验证平台

| 目标 | 现场证据 | 边界 |
| --- | --- | --- |
| Windows amd64 | Windows 11 10.0.26200、Go 1.26.5；真实 SCM 安装、Event Log、停止/重启与失败恢复 | 正式流量仍需受信任 TLS 反向代理 |
| Linux amd64 容器 | Docker Engine 29.6.1；scratch Gateway、PostgreSQL 18.4、Valkey 9.1.0、Caddy 2.10.2 双实例拓扑 | 证据来自隔离 Linux 容器环境，不冒充 owner 尚未提供的生产主机 |
| 管理端浏览器 | 有头 Chromium 桌面与 Pixel 7 视口，连接真实 Go、PostgreSQL、Valkey 和生产构建前端 | 视觉审美由 owner 验收；自动化只证明结构和交互事实 |
| 标准客户端 | OpenAI Go v3.44.0、Python openai 2.46.0 | Provider 当日可用性与额度不构成永久 SLA |

未列入本次正式发布目标：Linux arm64 原生部署、macOS 服务、Kubernetes、多地域高可用，以及图像、视频、语音、Embedding 和 Rerank 协议。

## 制品验证

Tag CI 生成 Windows/Linux amd64 压缩包、Linux OCI tar、Go/Node 许可证清单、SPDX/CycloneDX SBOM、`SHA256SUMS`、版本 manifest、in-toto/SLSA provenance 和 Sigstore bundle。正式 bundle 使用 GitHub OIDC keyless 身份并进入透明日志；本地验收只使用随工作目录销毁的临时私钥，不上传透明日志。

下载制品后执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-release.ps1 `
  -Directory .\.build\release-0.1.0 `
  -Keyless
```

验证必须同时通过 checksum、provenance subject、SBOM JSON 和签名身份；任一失败都拒绝部署。生产 Compose 中 Gateway、PostgreSQL、Valkey 和 Caddy 全部使用经审核的 `@sha256` 引用，不使用浮动标签。

## 安装与升级

首次安装、secret 文件权限、systemd 和 Windows SCM 见 `deploy/README.md`。升级顺序固定为：

1. 验证发布物、镜像 digest、磁盘空间、配置和当前 migration 状态。
2. 创建 PostgreSQL custom-format 备份并用 `pg_restore --list` 验证。
3. 独立执行前向 migration，再逐实例替换 Gateway；每个实例 readiness 通过后才继续。
4. 应用失败且 migration version 未变化时回到旧 digest。
5. migration version 已变化时禁止 image-only rollback；恢复备份到新数据库，核验后切换 database URL file。

## 已知外部依赖

正式域名、DNS、公网证书、生产主机、镜像仓库、GitHub OIDC 发布身份、外部 Prometheus/Grafana、通知渠道和异地加密存储位置必须由部署环境提供。缺少这些事实不会阻塞隔离验收，但必须在 `plan.md` 最终验收前逐项记录为已完成或准确的外部风险。
