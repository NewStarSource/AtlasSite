# 新星账户与星图身份契约 v0.2

本契约在 S01 定义，S03 已实现本地合成登录、会话与状态同步；生产 HTTPS 和真实配置仍在 S04。浏览器本地账户会话不是星图登录凭证，必须经过标准协议验证。

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

账户 Provider 使用 `github.com/zitadel/oidc/v3 v3.51.11`；星图 RP 使用 `github.com/coreos/go-oidc/v3 v3.21.0` 和 `golang.org/x/oauth2 v0.37.0`，以两个维护中的组件互操作测试标准协议。签名与验证使用成熟库。S02 密码哈希采用 x/crypto/bcrypt，Web CSRF 使用 gorilla/csrf，SMTP 使用 go-mail，数据库使用 modernc SQLite。

星图只接受 Authorization Code + PKCE S256。回调拟定 `/auth/callback`，客户端 confidential，精确白名单 redirect URI。每次请求必须生成随机 state、nonce 和 verifier，服务端短期保存并绑定发起浏览器；code/state 使用一次。校验 issuer、audience、签名、nonce、iat/exp、auth_time，限制时钟偏差 60 秒。issuer、JWKS 来源不得取自不可信浏览器参数。

OIDC 未配置时 `/login`、`/auth/callback` 返回 503 AUTH_NOT_CONFIGURED。完整配置后从固定 issuer discovery 读取端点，并校验所有端点 origin 一致。不得降级为邮件地址、前端 accountId 或开发会话登录。

## 状态与撤销

| 事件 | 星图必须实施的效果 | 恢复 |
|---|---|---|
| session_revoked(sid) | 撤销对应本地会话，重复事件幂等 | 新登录创建新会话 |
| sessions_revoked(account_id) | 撤销该账户全部本地会话 | 不复活旧凭据 |
| suspended | 立即停止互动和新登录，撤销会话 | 重新验证且状态恢复后新登录 |
| deactivated | 立即停用并开始 30 天恢复期；撤销全部会话/API 密钥、暂停 Webhook | 仅重新验证后的恢复、申诉、导出通道 |
| recovery_pending | 禁止普通登录/互动 | 账户服务验证成功后 active，新凭据 |
| deleted | 不可登录，后续数据清理按 TECH-11 | 不恢复旧身份凭据 |

内部事件 envelope 使用 `event_id`、`sequence` 单调序号、`occurred_at` UTC 秒、`account_id`、`status_version`、`event_type`、可选 `sid`；响应包含 `schema_version:1`。S03 仅在回环网络使用固定客户端 Basic Auth 拉取 `/internal/identity/events?after=N`，不用浏览器 Cookie 授权。`/internal/identity/session` 同时验证 account_id 与 sid，提供登录/访问时的最新状态。状态和撤销事件由数据库触发器同事务生成，星图应用事件和推进水位同事务；跨服务不共享数据库写权限。正式 HTTPS、独立服务凭据和传输保护需在 S04 验收。

TECH-09 要求账户服务不可用时，已验证且未过期、未被本地获知撤销的既有星图会话继续普通操作；新登录和敏感操作暂停。网络中断期间不能承诺立即得知远端撤销。S03 每两秒后台同步，并在有会话的请求中同步和复查 sid；返回身份服务不可用标记。重连补拉遗漏事件，不把同步失败解释为恢复 active。事件保存 30 天，星图会话最长 7 天；重连后的会话状态查询还会拒绝已不存在/失效的 sid。

## 错误

本地账户 API 使用 `{ok,code,message}`。REQUEST_ACCEPTED=202；INPUT_INVALID/LINK_INVALID=400；INVALID_CREDENTIALS=401；CSRF_INVALID/ORIGIN_INVALID=403；RATE_LIMITED=429（Retry-After）；DEPENDENCY_UNAVAILABLE/BUSY=503。注册和找回对存在、不存在、已验证账户使用相同 202 正文，不返回 ID、邮件状态或 token。

S03 内部额外使用 ACCOUNT_INACTIVE、SESSION_REVOKED、IDENTITY_UNAVAILABLE、REAUTH_REQUIRED。这些错误仅在已认证服务间传输；未认证登录统一提示，不用错误泄露邮箱是否存在。
