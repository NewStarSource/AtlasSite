# CI 工具链漏洞扫描修复（2026-10-09）

用户提供的 GitHub Actions 日志中，格式检查、`go test -race` 和 vet 已通过，失败发生在 `govulncheck`。`setup-go@v5` 依据 `go.mod` 安装 Go 1.26.6，扫描发现 11 个应用可达的标准库漏洞，涉及 net/http、net/textproto、crypto/tls 和 html/template，日志列出的 Go 1.26 分支修复版本均为 1.26.9。

[Go 官方发布记录](https://go.dev/doc/devel/release) 确认 2026-10-08 发布的 Go 1.26.9 与 1.27.2 包含相关安全补丁。此前 Windows 开发使用 Go 1.27.1，其历史通过结果不能替代新漏洞数据库下的验证。

## 修复

- 两个仓库的最低版本均提高到 `go 1.26.9`，并添加 `toolchain go1.27.2`。
- 工作流升级至 `actions/checkout@v5` 和 `actions/setup-go@v6`，处理日志中的 Node 20 弃用提示。setup-go v6 支持优先读取 toolchain 指令，CI 和默认自动选择模式下的本地环境都选择 Go 1.27.2。原日志 runner 为 2.337.0，满足该 Actions 版本的要求。
- README 更新工具链要求。业务代码、依赖版本、数据库和验证门槛未变；格式检查、race、vet 和漏洞扫描全部保留。

## 已验证

两个项目均实际输出 `go version go1.27.2 windows/amd64`。

- 两个仓库 `gofmt -l cmd internal` 无输出，差异格式检查通过。
- `pwsh -NoProfile -File scripts/check_core.ps1` 全部通过：两个仓库完整 Go 测试、vet、govulncheck、构建、S03 和核心双服务真实 HTTP 联调。
- 两个仓库 govulncheck 均为 0 个应用可达漏洞。未调用的依赖提示仍保留，未忽略扫描结果。
- `node scripts/test_ui_actions.cjs` 通过。

本机 CGO=0 且无 C 编译器，本轮未在 Windows 执行 race；工作流继续在 Ubuntu 上执行 `go test -race ./... -count=1`。未执行新提交的远端 GitHub Actions，因此远端最终结果须由推送后的新运行确认。重跑旧提交不会使用本次工具链配置。
