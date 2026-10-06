# StarAtlas 星图

按 `D:/NewStarProject/SAfiles/TECH-01` 的 Go + chi + SQLite 基线建设。
当前已补齐核心内容、用户权利和本地运维链路，仍限 development/test 回环环境。账户服务位于相邻 `StarAccount` 仓库。

本次交付见 [核心缺口补齐记录](docs/CORE_GAPS_2026-10-06.md)、[运行与恢复步骤](docs/RUNBOOK_CORE.md)、[代码差异复查](docs/REVIEW_CORE_2026-10-06.md)。数据库升级至 schema 6；升级前停止旧进程并保存迁移前副本，旧二进制不能直接打开新库。

后续已完成真实页面、统一样式和桌面/手机视口复查，页面入口、实际操作结果与截图见 [页面完善与浏览器复查](docs/UI_REVIEW_2026-10-06.md)。

一次执行两个仓库的 Go 测试、vet、漏洞扫描、构建及原始 HTTP 联调：

```powershell
pwsh -File scripts/check_core.ps1
```

脚本不进行图形浏览器复现。真实部署、全天告警、获授权示例和邀请观察的实际状态见交付记录。

## 启动

需要 Go 1.26.6 或更新版本（本次验证 Go 1.27.1）。在本目录执行：

```powershell
go mod download
go run ./cmd/staratlas init-dev
go run ./cmd/staratlas
```

初始化只需一次，已有配置不会覆盖。打开 http://127.0.0.1:4200 。
新星账户按其 README 在另一终端启动。不要混用 localhost 和 127.0.0.1，Host 校验严格匹配配置。
退出服务按 Ctrl+C。开发数据与配置保存在忽略的 `.local/`，自动化测试使用临时目录。
需要独立测试实例时将配置复制到 `.local/test.json`，修改 mode、端口、origin 和 database，再执行 `go run ./cmd/staratlas -config .local/test.json`。

## 验证

```powershell
go test ./... -count=1
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

健康检查 `/health` 验证数据库连接。首页 `/` 和 `/config` 提供配置清单。
未配置 OIDC 时 `/login`、`/auth/callback` 拒绝登录。配置后以 Authorization Code + PKCE 登录，`/api/v1/dev/session` 始终关闭，没有合成身份绕过入口。
`/api/v1/session` 只输出当前星图身份和降级状态。星图不读取、保存、转发密码，也不读取账户数据库。

见 [阶段交付](docs/S01-S02.md)、[身份契约](docs/identity-contract.md)、[配置清单](docs/configuration.md)、[审核记录](docs/review-S01-S02.md)。
本地 OIDC 验收不代表真实邮件、HTTPS 或生产环境已完成。

## S03 本地单点登录

先在 StarAccount 目录执行 `go run ./cmd/staraccount init-oidc`，再在本目录执行：

```powershell
go run ./cmd/staratlas init-oidc
go run ./cmd/staratlas
```

`init-oidc` 从相邻账户仓库复制一次客户端密钥到本仓库受限 `.local` 配置；运行时不读取账户仓库或其数据库。可追加参数指定账户客户端密钥文件路径，密钥内容不输出。账户 OIDC 签名私钥永不复制。
账户服务也需重新启动。打开 `/login`，完成账户确认后进入星图 `/security`。安全页面提供本地退出、重新认证及撤销全部星图会话。
后台每两秒拉取已认证身份事件，并在会话访问前同步；账户服务中断时既有会话继续普通读取，新登录和敏感操作暂停。

双服务集成测试使用临时目录和随机端口，不访问演示数据、不外发邮件：

```powershell
go build -o bin/staratlas-s03.exe ./cmd/staratlas
go -C ../StarAccount build -o bin/staraccount-s03.exe ./cmd/staraccount
python scripts/test_s03.py --account-bin ../StarAccount/bin/staraccount-s03.exe --atlas-bin bin/staratlas-s03.exe
```

详见 [S03 交付与验收](docs/S03.md) 和 [S03 审核](docs/review-S03.md)。邀请码注册与真实 SMTP、域名和 HTTPS 属于 S04，当前 production 仍拒绝启动。
