# S01/S02 AI 代码审核记录

审核日期：2026-10-02。实现之后单独重新读取账户 auth/app/store/mail、星图 app 和测试代码，依据正式规则与实际新增代码执行审核；两个仓库没有已提交基线，因此以新增文件内容为审查范围。该记录不是安全认证，也不替代真实服务联调。

## 检查结果

| 项目 | 结果 | 证据 |
|---|---|---|
| 两个服务独立启动 | 通过 | `go run ./cmd/staraccount`、`go run ./cmd/staratlas` 均在回环地址启动；配置缺失/生产模式拒绝启动 |
| 健康检查与配置页 | 通过 | `GET /health`、`/api/v1/config`，浏览器检查账户注册、合成收件箱和配置页 |
| SQLite 基线 | 通过 | WAL、foreign_keys、synchronous=FULL、busy_timeout=2000、单写连接、编号 001 初始化事务 |
| 密码与令牌边界 | 通过 | bcrypt cost 10；密码只在账户库；会话/一次性令牌只存 SHA-256；令牌随机 256 位且一次消费 |
| 邮件队列 | 通过 | AES-GCM 加密 payload；合成模式只读本机收件箱；SMTP 失败退避，第三次进入 permanent_failure |
| 注册与验证 | 通过 | 重复注册统一 202；旧验证链接失效；过期/重复链接返回 LINK_INVALID；验证成功撤销旧会话 |
| 登录与退出 | 通过 | 通用失败提示；HttpOnly/SameSite=Lax；退出和密码重置撤销旧会话；旧 cookie 不再认证 |
| 限流 | 通过 | IP + 接口、邮箱 + 接口；登录与找回达到阈值返回 RATE_LIMITED；不信任 X-Forwarded-For |
| Web 防护 | 通过 | CSRF token、Origin/Host 校验、跨站 Fetch 拒绝、no-store、CSP、X-Frame-Options、X-Content-Type-Options、noindex |
| 星图密码隔离 | 通过 | 星图账户库仅保留 account/subject 映射和未来会话表；登录、回调、开发 session 路由均关闭 |
| 数据清理 | 通过 | 过期 token、邮件、未验证账户、会话和 30 天安全事件清理；测试覆盖过期时间推进 |
| 恢复与迁移失败 | 通过 | 临时库、损坏库和不兼容 schema 启动失败；迁移事务回滚；不替换现有数据库 |

## 自动化命令

```text
StarAccount: go test ./... -count=1 -timeout=120s       PASS
StarAccount: go vet ./...                               PASS
StarAtlas:   go test ./... -count=1 -timeout=120s        PASS
StarAtlas:   go vet ./...                                PASS
```

`govulncheck@v1.8.0` 的结果是业务代码 0 个可达漏洞。账户依赖报告了 gorilla/csrf 的 TrustedOrigins 历史漏洞（代码未使用该配置，Origin 另行严格校验）和 x/crypto/openpgp 的不可达间接模块告警；Atlas 没有可达或依赖命中。依赖来源和版本进入两个 `go.sum`，后续升级前重跑扫描。

账户共 11 个测试、星图共 2 个测试。本机 race、远程 CI 尚未运行。两项依赖告警不能表述为“全部依赖没有漏洞”；使用 TrustedOrigins 或 OpenPGP 前须重新评估。Go 官方 GO-2025-3884 报告明确指出其触发条件为 TrustedOrigins 的 HTTP/HTTPS 来源混用，当前工程没有使用该选项。

## 浏览器证据

在 `http://127.0.0.1:4100` 实际完成：注册 `reader3@example.com`、打开本机合成邮件、打开 GET 验证页、提交验证、登录、首页看到 active 会话、退出并看到“旧会话已失效”。注册时使用的密码和邮箱属于合成测试数据，未提交到外部服务。

## 未完成和阻断项

- 未配置真实 SMTP、域名、HTTPS 或生产密钥；不能进入真实用户测试。
- 未实现 S03/S04 的 OAuth/OIDC Authorization Code + PKCE、issuer/audience/nonce/state/JWKS 校验、账户与星图状态事件同步和真实会话撤销联调。
- 未实现注销恢复、敏感操作重新认证、Passkey/TOTP、会话列表和管理额外保护。
- 本地 Windows 未启用 CGO，因此未宣称本机 race 检查通过；CI workflow 在 Ubuntu 执行 `go test -race`。
- 当前没有自动提交、推送、部署或外发邮件。

## 修复记录

初版 Node 草稿被移除，原因是与正式 Go/SQLite 架构基线不一致、测试进程生命周期不适合交付。之后补齐 Go 服务、临时库测试、迁移事务、浏览器表单、严格来源策略和旧验证链接失效处理；最终回归测试通过。

| 位置 | 发现与影响 | 修复与复查 |
|---|---|---|
| 原 Node 注册与持久化草稿 | 注册状态码区别新旧账户、星图 session 明文、内存/JSON 状态不满足规范 | 移除草稿；Go 注册统一响应、哈希令牌、SQLite 事务；回归注册/退出/持久化 |
| StarAccount auth.go / requestMail | 重复注册留下多个有效验证链接，可能确认旧密码 | 撤销旧 token，清除 candidate_hash，移除旧邮件；预注册、重放与并发消费测试通过 |
| StarAccount app.go / Referrer-Policy | no-referrer 导致浏览器 POST Origin=null，被严格来源检查拒绝 | 改 strict-origin，仅发送 origin、不发送路径和 query；保留拒绝 null/外站 Origin、CSRF 与 Fetch 检查；真实浏览器流程及表单测试通过 |
| 两个 store 初始化路径 | 多语句 schema 执行中失败可能留下部分表 | 编号 001、事务初始化、未知版本拒绝；不兼容迁移回滚测试通过 |
| go.mod / chi | 原代理选中旧 chi，模块扫描命中已修复的 RealIP 告警 | 升级 v5.3.0；未启用 RealIP middleware；扫描复查不再命中 |

SMTP 有至少一次投递语义：发送成功后进程中断可能再次发送同一链接，Message-ID 保持稳定；验证令牌本身只会消费一次。SMTP 服务端去重与真实投递未验证，不声称邮件绝对不重复。S10 的外部审计锚点、正式备份、Windows ACL/磁盘加密仍不在本阶段通过项内。
