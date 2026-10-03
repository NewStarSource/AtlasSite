# S03 AI 代码审核记录

审核日期：2026-10-02。范围为 StarAccount/StarAtlas 的 S03 新增代码、迁移、测试和启动工具；未读取真实凭据或生产数据。开发后单独复查授权/令牌接口、回调事务、事件应用、重新认证和库默认日志路径，以下记录具体发现与复查证据。

## 检查结论

| 检查 | 结果 |
|---|---|
| Go 1.26.6+ 编译、格式、单元测试 | 两仓库通过 |
| OIDC 授权码 + PKCE | 通过集成测试，错误 redirect/state/nonce/code verifier、过期和重放均拒绝 |
| 身份字段 | 只用 issuer + sub/subject_id 映射；邮箱和密码不进星图 |
| 回调绑定 | state、nonce、PKCE verifier 与浏览器绑定；授权码在数据库中单次消费 |
| 本地会话 | 令牌只存 hash；账户 sid、状态版本和撤销事件参与会话判断 |
| 状态同步 | 事件序号和状态版本幂等；旧版本不能覆盖新状态；同步失败保留普通已有会话并阻断新登录/敏感操作 |
| 敏感操作 | 最近 5 分钟重新认证；撤销会话和用户注销不能由普通会话直接执行。管理员停用仅有合成环境 CLI，无公开路由 |
| 注销恢复 | 30 天恢复期；重新验证密码；恢复不复活旧会话 |
| 协议日志 | OIDC 库日志替换为固定 `oidc_protocol_event`，不输出 URL、token、client secret 或授权码 |
| 数据迁移 | 账户和星图 001→002 迁移事务化，已有账户/映射保留 |

## 执行检查

```text
AtlasSite:   go test ./... -count=1 -timeout=120s  PASS
StarAccount: go test ./... -count=1 -timeout=120s  PASS
两仓库:      go vet ./...                       PASS
两仓库:      govulncheck                         业务代码可达漏洞 0
集成脚本:    scripts/test_s03.py                 PASS
```

最终统计：StarAccount 17 个顶层测试、StarAtlas 5 个顶层测试，共 22 个；星图回调测试额外包含 13 个安全子场景。Go 1.26.6 下执行测试和扫描，避免此前 CI 使用过旧标准库的问题。真实浏览器完成登录返回星图、强制重新输入密码、返回星图安全页，截图保存于忽略的 `.local/s03-login.jpg`。

govulncheck 仍可能报告未被当前代码调用的依赖模块告警；该结果不能表述为全部依赖无漏洞。当前日志和集成测试使用合成数据，未执行真实 SMTP、HTTPS 或生产配置测试。

## 阻断项

真实域名、HTTPS、真实 SMTP、生产 OIDC 密钥轮换、跨服务正式签名传输、管理入口额外保护和内容注销策略未完成。S03 只允许本地回环 test/development 配置，不能作为公测版本。

## 发现、修复和复查

| 位置 | 触发、影响 | 修复与证据 |
|---|---|---|
| StarAccount app.go / logout | JSON 请求体未被读取，Windows 真实 HTTP 客户端可能收到连接重置 | 在退出处理前有限读取正文；双服务退出/重启集成测试通过 |
| StarAccount oidc.go / AuthRequestByCode | 并发兑换同一个 code 可导致重复签发 | 条件 UPDATE 原子消费后才返回授权请求；重复 code/错误 PKCE 后重放被拒绝 |
| AtlasSite identity.go / callback | 失败路径在未释放单连接事务前写审计可能自锁 | 延后审计至事务回滚后；错误声明/重放测试设超时并通过 |
| AtlasSite identity.go / callback | 使用旧事件水位建立新会话可能被历史事件错误撤销 | 完成新会话前同步历史事件并核对状态版本与 sid；恢复后重新登录的集成测试通过 |
| AtlasSite identity.go / syncIdentity | 陈旧状态不能覆盖新状态，也不能再触发状态撤销副作用 | 按版本条件更新，只有新版本应用成功才按状态撤销；陈旧状态与幂等测试通过 |
| StarAccount protocol_log.go | OIDC 库默认日志会包含不可信重定向 URL，可能夹带敏感参数 | 白名单固定事件消息，忽略所有请求属性与原文；脱敏日志单元测试与失败集成日志复查通过 |
| 两站表单与跨站重定向 | CSP form-action self 会阻止表单 POST 后连续跳转到另一 origin | POST 返回明确继续页，用用户点击 GET 完成跨站返回；保留同源表单策略；浏览器实际验证首次登录与强制重新认证 |
| 002_identity.sql | 状态更新和撤销事件分开提交会丢事件 | 数据库触发器同事务入队；回滚事务不留下事件的测试通过 |

## 验证边界

单元测试含真实签名 JWT 夹具，覆盖错误签名、issuer、audience、nonce、未来/过期时间、未验证邮箱、非 active、缺失主体、无效 auth_time、at_hash 和 ID token 重放。集成脚本使用两个真实进程，覆盖错误浏览器、state、nonce、redirect、PKCE、过期/重复 code，以及降级、重启、撤销和恢复。

本机 Windows CGO 未开启，未运行 race；Ubuntu CI 的 race 工作流已配置但远程结果尚未验证。双服务集成脚本需两个仓库和二进制，当前各仓库 CI 不自动检出另一私有仓库，需人工运行该脚本。身份事件保留 30 天，正式备份恢复、外部审计锚点和回滚后的事件水位治理仍在 S10。
