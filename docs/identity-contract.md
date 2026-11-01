# 星图与账户身份约定

两个服务独立部署、独立数据库。账户持有用户名、密码哈希、恢复码哈希、登录会话和签名私钥；星图只持有稳定身份映射、化名、状态和自己的会话。运行时星图通过已认证 HTTP 接口读取身份状态与公开资料，不读取账户仓库或数据库。

## OIDC

- 客户端固定为 `star-atlas`，Authorization Code + PKCE S256，只支持 openid/atlas_identity。
- issuer 为账户公开 origin，redirect URI 精确为星图 origin + `/auth/callback`；发现得到的授权/token/JWKS 地址必须属于配置账户 origin。
- state、nonce、PKCE verifier、浏览器流程 Cookie 共同绑定流程；code 单次交换、五分钟过期。账户 RSA RS256 签名密钥至少 2048 位，客户端秘密与签名私钥分离。
- 生产只允许 HTTPS issuer/回调，关闭 OIDC DevMode/WithAllowInsecure。开发和测试仅允许回环 HTTP。
- ID token 验证签名、issuer、audience、nonce、时间、at_hash、稳定 subject、账户状态和验证标志。`email_verified` 为兼容的账户验证标志；邀请码注册并未验证真实邮箱，不应把该 claim 当成已核实邮箱。返回身份不含密码或真实邮箱。
- access token 为短期 opaque token，仅保存哈希；没有 refresh-token grant。星图会话与账户 session ID 关联。

## 会话与撤销

账户会话有效七天；敏感操作需要五分钟内重新认证。原始会话、恢复码和一次性 token 不入库，只保存哈希。生产相关 Cookie 为 Secure/HttpOnly/SameSite=Lax。开发显式使用非 Secure 回环 Cookie。

星图后台拉取经过客户端认证的状态/撤销事件，并在已登录请求时同步。账户撤销、停用、注销在正常联通时传播到星图。账户不可达时，既有有效会话可继续普通读取；新登录、写入与敏感操作暂停。不能把降级读取称为实时确认账户状态。

密码重置会撤销所有账户会话，进而使相关星图会话失效。恢复码仅单次、最长一年有效，密码验证后替换会使旧码失效。找回不能解除 suspended/deactivated/deleted。恢复已注销账户限 30 天且要求原密码；删除的内容不复活。

## HTTP 边界

两端的 Origin 必须精确匹配公开配置；写请求要求 CSRF。账户允许跨站的安全 GET/HEAD 顶层导航，拒绝跨站写入和跨站资源请求。OIDC 仍要求 state/nonce/PKCE 与流程 Cookie，允许导航不会放开写权限。

生产监听回环，由可信本机代理提供唯一客户端 IP 和 https 协议声明；不接受任意 X-Forwarded-For 链或任意 Forwarded Host。认证和协议限流使用经校验的客户端地址。具体配置见部署手册。

## 内部接口

- `GET /internal/identity/events?after=<sequence>`：有序状态与会话撤销事件，不公开恢复码或凭据变更细节。
- `GET /internal/identity/session?account_id=...&sid=...`：当前会话与身份状态。
- `GET /internal/identity/profile?account_id=...`：公开资料，简介由星图按安全 Markdown 规则重新渲染。
- `POST /internal/identity/deactivate`：由经过重新认证的星图注销操作调用。

内部接口要求客户端 Basic 认证，秘密不得输出。生产调用仍经正式 HTTPS 入口，不提供可伪造身份的开发 session API。

## 恢复与数据版本

账户 schema 6，星图当前代码 schema 8。普通账户备份清除恢复码、会话与授权/邮件 token，支持读取 schema 5/6 账户备份；星图支持 schema 6/7/8 普通备份。最新独立签名清单不可随主库回滚。回放凭据变更会禁用旧密码并进入 recovery_pending；旧恢复码不能复活。该状态仅可由独立核验后的本地受控命令签发短期码恢复。

星图恢复后清除团队与代表授权，需重新确认权限。备份之后发生过修改的社群工具会撤下旧快照版本，写入恢复复核记录，不能自动重新发布旧公告或旧指引。合集版本仅保存原帖 ID 与顺序，读取时仍检查原帖的当前可见性。

完整短期灾难恢复包需要主库、私库、清单、锚点和密钥整体保护；它与长期普通快照有不同保留期限。
