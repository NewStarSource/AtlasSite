# StarAtlas 星图

Go + chi + SQLite 社群与内容服务。账户由相邻的 StarAccount 独立持有，星图不保存密码。当前代码的数据库版本为 **10**，账户数据库版本为 **6**。生产采用单台 Ubuntu 24.04、本机 TLS 反向代理与独立服务用户部署；十一月本地开发改动尚未部署，服务器实际版本需另行核对。

## 本地启动

使用 Go **1.27.3 或更新的修复版本**；模块最低 Go 1.26.10，推荐工具链由 go.mod 指定。两个仓库并列放置。

```powershell
# StarAccount 目录
 go run ./cmd/staraccount init-dev
 go run ./cmd/staraccount init-oidc
 go run ./cmd/staraccount generate-invite
 go run ./cmd/staraccount
# AtlasSite 的另一终端
 go run ./cmd/staratlas init-dev
 go run ./cmd/staratlas init-oidc
 go run ./cmd/staratlas
```

打开 http://127.0.0.1:4200，从星图发起登录。注册需要一次性邀请码；注册成功后立即离线保存恢复码，已有账户可在账户安全页验证密码后生成。不要混用 localhost 与 127.0.0.1。初始化不会覆盖已有配置与密钥，开发数据位于忽略的 `.local/`。

## 检查和发布构建

```powershell
pwsh -File scripts/check_core.ps1
node scripts/test_ui_actions.cjs
# 新输出目录；依次执行两端 test/vet/漏洞扫描、构建、双服务 HTTP 与 JS 回归
python scripts/check_release.py --output .local/release-unique
```

Linux CI 可加 `--race`。release 目录包含 Linux amd64 程序、manifest.json、部署模板与运维脚本；manifest 记录两个仓库的 commit、未提交状态、工具链与 SHA-256。部署前将审阅通过的改动提交，使用明确版本重建，不发布 dirty 产物。双服务工作流 `.github/workflows/release.yml` 要求填写两个精确 commit 和账户仓库；私有账户仓库使用只读 `ACCOUNT_READ_TOKEN`。StarAccount 当前本地未配置远端，需要先创建并推送其仓库，才能运行这一远端工作流。

2026-10-24 本地验收通过两端 test/vet、Go 1.27.3 下 govulncheck（可达漏洞 0）、双服务 HTTP/JS 回归、真实 Chrome 1440px/390px 注册与找回流程，以及 Ubuntu 24.04 子系统中采用临时可信 CA 的生产 HTTPS/OIDC 联调。Windows 未运行 race，服务器原生 Linux race、双服务完整联调及真实域名 Chrome 桌面/手机验收也已通过。站点现已部署至 https://atlas.runnewstar.xyz 和 https://account.runnewstar.xyz，服务器地址 38.246.251.26，SSH 12721，已验证整机重启后恢复。外部通知与异机恢复仍待接入实际目的地，当前仅有本机加密备份。

## 身份与访问规则

- [身份约定](docs/identity-contract.md)：OIDC、数据边界、撤销与降级行为。

首页、社群动态、个人内容、收藏、搜索和通知支持分页。全站继续保持 noindex，遵循 SA3 的站外索引限制；私密内容仍需服务端权限检查。

管理面板的社群资料页可进入公告、指引和合集维护。支持有效代表及团队人员编辑，包含草稿、发布、撤下、公告置顶、排序及修改记录。合集只引用本社群原帖，撤回或隐藏原帖后停止展示。整理者需团队登记既有正式成员资格核验结论，由代表按工具和期限邀请，本人从“我的 → 社群协作”主动接受。协作不授予人员管理或原帖编辑权，资格登记不创建正式成员或投票资格。

Wiki 发现页原型使用独立的 `wiki_enabled` 开关，默认关闭。目前支持公开来源、脚注、共同编辑、版本差异、多层讨论及隐藏和删除隔离，内容不进入普通动态。正式条目模板、人工审核和建议修改仍待开发。开启范围与验证方式见 [Wiki 内部原型说明](docs/WIKI_PROTOTYPE.md)。

## 月度记录

2026 年 11 月暂不公测，继续内部使用、功能试做与运行验证。此安排不自动改变服务器的访问控制配置。

- [2026.10 发行笔记](docs/RELEASE_NOTES_2026-10.md)
- [2026.11 发行计划](docs/RELEASE_PLAN_2026-11.md)
- [2026.11 开发记录](docs/DEVELOPMENT_2026-11.md)
