# 正式部署

主部署目标是单台 Linux 主机上的 Docker Compose：Caddy 是唯一公开入口，两个 Gateway 逐实例替换；PostgreSQL、Valkey 只加入内部网络。目标容量基线是 300 个受控账号、约 60 个持续活跃用户，不代表跨地域高可用。正式公网部署仍需 owner 提供域名、DNS、主机、证书/ACME 邮箱和镜像仓库。

## Linux 首装

1. 安装受支持的 Docker Engine 与 Compose plugin，启用 Docker 自启动；准备至少 4 vCPU、8 GiB RAM 和独立持久磁盘。容量基线使用 32 逻辑 CPU/15.5 GiB，较小主机必须重新跑容量门槛。
2. 把 `production.env.example` 复制到 `/root/llmgateway-deployment.env`，设为 `root:root 0600`，填入 `image@sha256:digest`、正式域名和八个固定 secret 路径。该源文件必须位于 `/etc/llmgateway` 外，全部祖先由 root 拥有且不可被 group/world 写入；environment 只保存路径，secret 内容不得进入 environment、shell history 或工单。
3. 先创建 `/etc/llmgateway/secrets` 和八个非空普通文件；PostgreSQL DSN 指向 Compose service `postgres:5432`，Valkey ACL 使用 `user default on >PASSWORD ~* &* +@all`，其密码与 Gateway 的 Valkey password 文件一致。使用 secret 管理设施写入内容并先收紧为 root only，安装器随后按容器身份重建精确权限。
4. 以 root 执行 `./install-linux.sh /root/llmgateway-deployment.env`。安装器固定文件到 `/opt/llmgateway/deploy`，把运行配置收敛为唯一的 `/etc/llmgateway/deployment.env` 与精确 secret 树，先启动存储和独立 migration，再安装并启用 `llmgateway-compose.service`。安装成功后删除 `/root/llmgateway-deployment.env`，不要保留第二份活动配置。
5. DNS 生效后检查 `https://域名/health/live`、`/health/ready`、登录页证书链和 Caddy/Gateway JSON 日志。只有 readiness 为 200 的实例进入代理池。

`Caddyfile.internal` 和 `compose.acceptance.yaml` 只用于隔离验收的 internal CA，不用于公网。生产 `Caddyfile` 使用 Caddy 自动 HTTPS；80/443 之外的公开监听必须单独审查防火墙和可信代理地址。

生产配置不得复制 Windows 开发入口为透明代理添加的 Fake-IP 网段。Provider 公网地址允许按权威 DNS 正常变化，但每次解析和重定向仍由 Gateway 的 SSRF-safe transport 校验；生产私网访问只有经过单独威胁建模和明确配置后才能放行。

## 升级与回滚

```bash
sudo /opt/llmgateway/deploy/upgrade-linux.sh \
  registry.example.test/llmgateway@sha256:NEW_DIGEST \
  /var/backups/llmgateway/pre-upgrade-YYYYMMDDHHMMSS.dump \
  https://gateway.example.test/health/ready
```

首次升级前先执行 `sudo install -d -o root -g root -m 0700 /var/backups/llmgateway`。升级器持有全体维护操作共用的独占锁，要求该目录为 `root:root 0700`、全部祖先由 root 拥有且不可被 group/world 写入，并要求至少有数据库大小两倍空间。它以 `0600` staged 文件生成并验证 custom-format dump，拒绝 symlink、hardlink 和跨文件系统异常，封存为 `0400` 后在同一目录原子发布；上次强杀留下的 staged 文件只有继续满足同一 inode 安全合同时才会清理。随后脚本记录 Goose migration 版本，再依次替换 `gateway-a`、`gateway-b`。每次替换都必须同时通过容器 readiness 和可选公网 TLS health。

候选实例失败且 migration 版本未变化时，脚本自动把该实例恢复到旧 digest。migration 版本已经变化时禁止 image-only rollback：保留仍健康的实例，把升级前 dump 恢复到一个新数据库，核验 migration/管理员事实后更新 `database-url` secret，再逐实例重建 Gateway。不要覆盖或删除原数据库，回切完成前保留备份和两个库。

## 加密备份与灾备

创建 `root:root 0700` 的 `/etc/llmgateway-backup`，把 `backup.env.example` 复制到其中的 `backup.env`，权限设为 `root:root 0600`。repository、Restic password、AWS credentials 和可选 AWS config 都必须是 `root:root 0400/0600` 的非符号链接普通文件，全部祖先由 root 拥有且不可被 group/world 写入；这些控制文件必须位于 `/etc/llmgateway` 和 staging 之外，且不能互相重叠。production 只接受远端 S3 Restic URL，local repository 只允许显式 acceptance 演练。

运行配置树的权限合同为：目录和 `secrets` 是 `root:root 0750`，`deployment.env` 是 `root:root 0640`，PostgreSQL password 是 `root:root 0400`，Gateway secret 是 `65532:65532 0400`，Valkey ACL 是 `999:1000 0400`。备份内配置副本全部转换成 root only；恢复配置安装器会从该副本重建运行权限，不能手工批量改成同一个 UID/GID。

```bash
sudo ./initialize-backup-linux.sh \
  /etc/llmgateway-backup/backup.env \
  --confirm-backup-repository-initialization
sudo ./install-backup-linux.sh \
  /etc/llmgateway-backup/backup.env \
  --confirm-backup-schedule
systemctl list-timers llmgateway-backup.timer llmgateway-backup-freshness.timer
journalctl -u llmgateway-backup.service
```

首次安装 schedule 前必须用上面的显式命令初始化并完整检查空仓库；安装器不会在普通安装中隐式创建或覆盖 repository。安装成功后，systemd 使用稳定 launcher 和原子 bundle，避免正在执行的备份被脚本升级切断。

定时器每 2 小时触发并附加最多 10 分钟随机延迟，失败 10 分钟后重试一次；独立 freshness timer 每 15 分钟检查最后成功恢复点。备份保存 PostgreSQL custom dump、恢复配置、校验和和 manifest，执行 7 daily、5 weekly、12 monthly retention、配置的 pack subset check，并无条件删除明文 staging 和本次 Restic 容器。RPO 目标 6 小时，RTO 目标 2 小时。每班还应通过稳定 launcher 执行或核对完整 repository check：

```bash
sudo /opt/llmgateway/backup-bundle-launcher-linux.sh \
  check-repository /etc/llmgateway-backup/backup.env
```

正式 repository 必须是另一故障域的加密 backend；本机目录只允许 acceptance 演练，不能作为异地完成证据。

灾备时先停止旧入口写流量，在新主机准备 Docker、经过签名和校验和验证的同版本 release，以及只包含 repository/password/远端凭据的 backup control 文件。下面的 `/srv/llmgateway-release/deploy` 代表该已验证 release 中的 deploy 目录；不要从受损主机复制脚本，也不要使用隐式 `latest`：

```bash
sudo /srv/llmgateway-release/deploy/list-backups-linux.sh \
  /etc/llmgateway-backup/backup.env
sudo /srv/llmgateway-release/deploy/restore-backup-linux.sh \
  /etc/llmgateway-backup/backup.env \
  0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  /var/lib/llmgateway-restore \
  --confirm-disaster-restore
sudo /srv/llmgateway-release/deploy/install-restored-configuration-linux.sh \
  /var/lib/llmgateway-restore/backup/configuration \
  /etc/llmgateway \
  /root/llmgateway-restored-database-url \
  llmgateway_restored \
  --confirm-restored-configuration-install
sudo /srv/llmgateway-release/deploy/restore-postgres-linux.sh \
  /etc/llmgateway/deployment.env \
  /var/lib/llmgateway-restore/backup \
  llmgateway_restored \
  --confirm-new-database-restore
```

`llmgateway-restored-database-url` 必须由 secret 管理设施创建为 `root:root 0400/0600`、单个 canonical PostgreSQL URI 且数据库名与目标一致；恢复配置目标必须不存在。数据库恢复时除 PostgreSQL 外，同一 Compose project 的所有已存在服务都必须处于 `exited`，paused/restarting/running 等状态会在创建目标数据库前失败。目标库已存在时默认拒绝；只有上次恢复留下的不完整库才能在额外确认开关下删除重建。

数据库恢复通过 manifest migration 核对后，把 `/etc/llmgateway/deployment.env` 复制成 `/root` 下的 root-only 安装源，执行同版本 `install-linux.sh`，成功后删除安装源和临时 database URL 文件。随后按 PostgreSQL、Valkey、Gateway、Caddy 顺序核对启动结果，完成管理员与成员登录、成员管理 API 403、资源池、套餐版本、订阅、API 密钥、上游 API Key 解密、额度和账本核对。确认新站点证书与 readiness 后才切 DNS/TLS；回切仍指向已验证的新数据库并逐实例替换，不能覆盖原库或把旧应用直接接到已变化的 schema。正式切流前记录 manifest 恢复点和端到端恢复时长。

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
- 有头桌面浏览器完成管理员初始化/重登、管理员创建成员并分配订阅、成员直接访问管理员 URL/API 被拒绝；标准 SDK 通过 Models/Chat/Responses/stream/tools/reasoning/cancel/error。
- 宿主按依赖顺序重启后资源池、套餐版本、订阅、成员、API 密钥、额度和账本仍在；Valkey 丢失只影响短期协调，不伪造持久事实。
- 外部 Prometheus 从 backend 网络分别抓取两个 Gateway 的 `/metrics`，Grafana 导入 `observability/grafana-dashboard.json`，Prometheus 加载 `observability/prometheus-rules.yaml`；公网 Caddy 的 `/metrics` 必须保持 404。正式监控主机、通知渠道和证书告警属于部署环境依赖，未配置时不得声称已接入值班。
- 账号恢复、API 密钥更换和指标告警按 `observability/runbook.md` 执行；只有目标环境的备份 timer 最近一次成功、远端仓库完整 check 和恢复演练均有现场证据，才能把该部署标记为灾备就绪。

固定版本验证规则可被 Prometheus 加载、看板可被 Grafana 导入：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-observability.ps1
```
