# StarAtlas 星图

按 `D:/NewStarProject/SAfiles/TECH-01` 的 Go + chi + SQLite 基线建设。
当前交付 S01 和 S02 的本地合成验收范围。账户服务位于相邻 `StarAccount` 仓库。

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
`/login`、`/auth/callback`、`/api/v1/dev/session` 均拒绝登录；本阶段没有 OIDC 或合成身份绕过入口。
`/api/v1/session` 始终未登录。星图不读取、保存、转发密码，也不读取账户数据库。

见 [阶段交付](docs/S01-S02.md)、[身份契约](docs/identity-contract.md)、[配置清单](docs/configuration.md)、[审核记录](docs/review-S01-S02.md)。
本地验收不代表真实邮件、生产环境、OAuth/OIDC 已完成。
