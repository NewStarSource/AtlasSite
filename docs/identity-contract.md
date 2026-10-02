# 新星账户与星图身份契约 v0.1

本契约为 S01 定义，S03/S04 实现。所有未来身份路径在实现前保持关闭。浏览器本地账户会话不是星图登录凭证。

## 所有权与字段

| 字段 | 归属与约束 | 公开性 |
|---|---|---|
| `iss` + `sub` / `account_id` | 新星账户签发，UUID 不因化名/邮箱修改而改变；星图按 issuer+sub 映射唯一身份 | 账户内部 ID 不作为自然人身份 |
| `subject_id` | 独立 UUID，同一已知主体关联依据另受授权；不凭邮箱推断多号同人 | 私密，不进入网页、公共 API 或日志 |
| `alias` | 化名快照，非唯一登录标识；修改记录在后续身份模块实现 | 可公开 |
| `email_verified` | 只有账户服务完成一次性验证后为 true | 登录断言使用，星图无需接收邮箱地址 |
| `status` | active/suspended/deactivated/recovery_pending/deleted | 具体处罚原因不向公共页面暴露 |
| `status_version` | 单调递增整数，丢弃重复/旧事件 | 内部 |
| `sid` / `account_session_id` | 账户会话 UUID，与星图本地会话关联，支持单会话撤销 | 内部 |
| `auth_time` / `iat` / `exp` | UTC 秒，重新认证依据，不由浏览器决定 | 内部 |
| `token_hash` | 星图本地会话随机值的 SHA-256；不保存明文 cookie | 内部 |

## 登录协议

拟采用维护中的 `github.com/zitadel/oidc/v3` 的 OP/RP 能力实现标准协议，S03 才加入依赖并验证兼容性；不自行实现 JWT 签名或 OAuth 协议。S02 密码哈希实际采用 `golang.org/x/crypto/bcrypt`，Web CSRF 使用 gorilla/csrf，SMTP 使用 go-mail，数据库使用 modernc SQLite。

星图只接受 Authorization Code + PKCE S256。回调拟定 `/auth/callback`，客户端 confidential，精确白名单 redirect URI。每次请求必须生成随机 state、nonce 和 verifier，服务端短期保存并绑定发起浏览器；code/state 使用一次。校验 issuer、audience、签名、nonce、iat/exp、auth_time，限制时钟偏差 60 秒。issuer、JWKS 来源不得取自不可信浏览器参数。

OIDC 未配置或未验证时 `/login`、`/auth/callback` 返回 503 AUTH_NOT_CONFIGURED。不得降级为邮件地址、前端 accountId 或开发会话登录。

## 状态与撤销

| 事件 | 星图必须实施的效果 | 恢复 |
|---|---|---|
| session_revoked(sid) | 撤销对应本地会话，重复事件幂等 | 新登录创建新会话 |
| sessions_revoked(account_id) | 撤销该账户全部本地会话 | 不复活旧凭据 |
| suspended | 立即停止互动和新登录，撤销会话 | 重新验证且状态恢复后新登录 |
| deactivated | 立即停用并开始 30 天恢复期；撤销全部会话/API 密钥、暂停 Webhook | 仅重新验证后的恢复、申诉、导出通道 |
| recovery_pending | 禁止普通登录/互动 | 账户服务验证成功后 active，新凭据 |
| deleted | 不可登录，后续数据清理按 TECH-11 | 不恢复旧身份凭据 |

内部事件 envelope 约定 `event_id` UUID、`schema_version:1`、`occurred_at` UTC、`account_id`、`status_version`、`event_type`、可选 `sid`。S03 使用受认证服务端通道/签名校验，禁止浏览器直调状态事件。事件接收、状态变更及撤销须同事务，跨服务不共享数据库写权限。

TECH-09 要求账户服务不可用时，已验证且未过期、未被撤销的既有星图会话继续普通操作；新登录和敏感操作暂停。不能将同步失败解释为恢复 active，也不能无条件清除限制。重试保留事件水位，重连补拉遗漏事件。此行为尚未实现，S03 必须测试故障与恢复。

## 错误

本地账户 API 使用 `{ok,code,message}`。REQUEST_ACCEPTED=202；INPUT_INVALID/LINK_INVALID=400；INVALID_CREDENTIALS=401；CSRF_INVALID/ORIGIN_INVALID=403；RATE_LIMITED=429（Retry-After）；DEPENDENCY_UNAVAILABLE/BUSY=503。注册和找回对存在、不存在、已验证账户使用相同 202 正文，不返回 ID、邮件状态或 token。

S03 内部额外使用 ACCOUNT_INACTIVE、SESSION_REVOKED、IDENTITY_UNAVAILABLE、REAUTH_REQUIRED。这些错误仅在已认证服务间传输；未认证登录统一提示，不用错误泄露邮箱是否存在。
