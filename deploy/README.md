# 正式部署

主部署目标是单台 Linux 主机上的 Docker Compose：Caddy 是唯一公开入口，两个 Gateway 逐实例替换；PostgreSQL、Valkey 只加入内部网络。目标容量基线是 300 个受控账号、约 60 个持续活跃用户，不代表跨地域高可用。正式公网部署仍需 owner 提供域名、DNS、主机、证书/ACME 邮箱和镜像仓库。

## Linux 首装

1. 安装受支持的 Docker Engine 与 Compose plugin，启用 Docker 自启动；准备至少 4 vCPU、8 GiB RAM 和独立持久磁盘。容量基线使用 32 逻辑 CPU/15.5 GiB，较小主机必须重新跑容量门槛。
2. 把 `production.env.example` 复制到仓库外，填入 `image@sha256:digest`、正式域名和八个 secret 文件的绝对路径。文件内容不得进入 environment 文件、shell history 或工单。
3. PostgreSQL DSN 指向 Compose service `postgres:5432`；Valkey ACL 文件使用 `user default on >PASSWORD ~* &* +@all`，其密码与 Gateway 的 Valkey password 文件一致。
4. 以 root 执行 `./install-linux.sh /etc/llmgateway/deployment.env.source`。安装器固定文件到 `/opt/llmgateway/deploy`，收紧 UID/权限，先启动存储和独立 migration，再安装并启用 `llmgateway-compose.service`。
5. DNS 生效后检查 `https://域名/health/live`、`/health/ready`、登录页证书链和 Caddy/Gateway JSON 日志。只有 readiness 为 200 的实例进入代理池。

`Caddyfile.internal` 和 `compose.acceptance.yaml` 只用于隔离验收的 internal CA，不用于公网。生产 `Caddyfile` 使用 Caddy 自动 HTTPS；80/443 之外的公开监听必须单独审查防火墙和可信代理地址。

## 升级与回滚

```bash
sudo /opt/llmgateway/deploy/upgrade-linux.sh \
  registry.example.test/llmgateway@sha256:NEW_DIGEST \
  /var/backups/llmgateway/pre-upgrade-YYYYMMDDHHMMSS.dump \
  https://gateway.example.test/health/ready
```

升级器持有独占锁，要求备份目录至少有数据库大小两倍空间，生成并验证 custom-format 备份，记录 Goose migration 版本，再依次替换 `gateway-a`、`gateway-b`。每次替换都必须同时通过容器 readiness 和可选公网 TLS health。

候选实例失败且 migration 版本未变化时，脚本自动把该实例恢复到旧 digest。migration 版本已经变化时禁止 image-only rollback：保留仍健康的实例，把升级前 dump 恢复到一个新数据库，核验 migration/管理员事实后更新 `database-url` secret，再逐实例重建 Gateway。不要覆盖或删除原数据库，回切完成前保留备份和两个库。

## 加密备份与灾备

把 `backup.env.example` 复制到 `/etc/llmgateway-backup/backup.env`，权限设为 `root:root 0600`。repository、Restic password 和远端存储凭据必须位于 `/etc/llmgateway` 之外并分别使用 file source；`LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE` 指向正式部署环境文件。配置目录中的运行 secret 由安装器设置为对应容器可读，恢复脚本统一重建为 `root:65532 0640`；Restic 控制密码始终仅 root 可读。

```bash
sudo ./install-backup-linux.sh \
  /etc/llmgateway-backup/backup.env \
  --confirm-backup-schedule
systemctl list-timers llmgateway-backup.timer
journalctl -u llmgateway-backup.service
```

定时器在每日 00:15、06:15、12:15、18:15 附加最多 15 分钟随机延迟，保存 PostgreSQL custom dump 与恢复所需配置，执行 7 daily、5 weekly、12 monthly retention 和仓库检查，并无条件删除明文 staging。RPO 目标 6 小时，RTO 目标 2 小时。正式 repository 必须是另一故障域的加密 backend；本机目录只允许演练，不能作为异地完成证据。

灾备时先在新主机准备 Docker、固定镜像和只包含 repository/password/远端凭据的 backup control 文件，然后只恢复到空目录：

```bash
sudo /opt/llmgateway/deploy/restore-backup-linux.sh \
  /etc/llmgateway-backup/backup.env \
  /var/lib/llmgateway-restore \
  --confirm-disaster-restore
sudo /opt/llmgateway/deploy/restore-postgres-linux.sh \
  /var/lib/llmgateway-restore/backup/configuration/production.env \
  /var/lib/llmgateway-restore/backup/postgres.dump \
  llmgateway_restored \
  --confirm-new-database-restore
```

把恢复后的 `database-url` 指向新数据库，执行独立 migration/status，按 PostgreSQL、Valkey、Gateway、Caddy 顺序启动，再完成管理员与成员登录、权限、Key、Provider 和账本核对。确认新站点证书与 readiness 后才切 DNS/TLS；回切仍指向已验证的新数据库并逐实例替换，不能覆盖原库或把旧应用直接接到已变化的 schema。正式切流前记录最后成功快照时间和实际恢复时长。

## Windows 服务

Windows 使用生产构建的同一 `webembed` 二进制。以提升权限的 PowerShell 准备 `windows-service.env.example` 对应的文件 secret，先执行 `dbtool -action up`，再运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows-service.ps1 `
  -BinaryPath C:\Program Files\LLMGateway\llmgateway.exe `
  -EnvironmentFile C:\ProgramData\LLMGateway\service.env `
  -Start -HealthURL http://127.0.0.1:8080/health/ready
```

安装器先执行 `--check-config`，拒绝 inline secret，注册虚拟服务账户、Event Log、延迟自启动和两次有界失败重启，并把 secret ACL 收敛到服务账户/SYSTEM/Administrators。正式用户流量仍必须经过同机或受信任前置 TLS 代理。卸载命令只删除 SCM/Event Log source，不删除数据库、secret 或日志。

## Production Checklist

- 镜像是已验证 release 的 `@sha256`，SBOM、签名/provenance、校验和与发布说明一致。
- 主机时间同步、磁盘告警、Docker 日志轮换、防火墙、DNS、TLS 续期和 ACME 联系邮箱已实测。
- 八个 secret 文件不在仓库/镜像/environment/日志；主密钥 ring 同时包含回滚所需版本并有异地密文备份。
- 独立 migration 成功后才替换应用；升级前 dump 已通过 `pg_restore --list`，恢复目标库名从未覆盖当前库。
- 两个 Gateway、PostgreSQL、Valkey 和 Caddy 均健康；Caddy 只代理 readiness 通过的实例，SSE 首字节和 30 秒流不被缓冲。
- 有头浏览器完成管理员初始化/重登、邀请/审核成员、成员直接访问管理员 URL/API 被拒绝；标准 SDK 通过 Models/Chat/Responses/stream/tools/reasoning/cancel/error。
- 宿主按依赖顺序重启后配置 revision、用户、Key、额度和账本仍在；Valkey 丢失只影响短期协调，不伪造持久事实。
- 外部 Prometheus 从 backend 网络分别抓取两个 Gateway 的 `/metrics`，Grafana 导入 `observability/grafana-dashboard.json`，Prometheus 加载 `observability/prometheus-rules.yaml`；公网 Caddy 的 `/metrics` 必须保持 404。正式监控主机、通知渠道和证书告警属于部署环境依赖，未配置时不得声称已接入值班。
- 账号恢复、Key 更换和指标告警按 `observability/runbook.md` 执行；备份 timer 最近一次成功、远端仓库 check、恢复演练和成本对账在对应生产切片完成前不得标记已就绪。

固定版本验证规则可被 Prometheus 加载、看板可被 Grafana 导入：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-observability.ps1
```
