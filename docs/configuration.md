# 配置清单

本轮用户已确认只验收本地合成环境，无需现在提供域名、SMTP 凭据或云资源。

真实邮件的服务商候选、自建选项和接入步骤统一记录在 [SMTP 后续待办](smtp-options.md)，稍后在 S04 联调前处理。

| 项目 | 当前本地值/用途 | 后续需要 |
|---|---|---|
| 星图地址 | http://127.0.0.1:4200，配置 mode/address/origin | 独立测试与正式 HTTPS 域名 |
| 账户地址 | http://127.0.0.1:4100 | 独立测试与正式 HTTPS 域名 |
| 数据库 | 两库各自 `.local/development.db`，WAL/FULL/FK/busy_timeout 2 秒，单连接写入 | VPS 本地 SSD，受限目录与备份位置 |
| 环境 | development；测试使用 t.TempDir；production 禁止启动 | 独立测试配置、数据、域名与密钥 |
| key_file | 账户 init-dev 生成 96 随机字节；前 32 CSRF，中 32 邮件加密，后 32 限流 HMAC | 与备份分离保存，部署/轮换流程 |
| 邮件 | synthetic，合成收件箱 /dev/mailbox | SMTP host、port（STARTTLS）、from；用户名/密码设置环境变量 |
| OIDC | S03 本地已实现，可用 init-oidc 初始化 | S04 配置正式 issuer、客户端、HTTPS、密钥与回调白名单 |
| 管理入口 | 未提供任何生产管理路由 | Passkey 或 TOTP 等可靠额外验证与恢复方案 |
| 对象存储 | 本阶段不使用 | 私有图片桶、备份桶、最小权限和生命周期 |

SMTP 账户与密码由 `STARACCOUNT_SMTP_USER` / `STARACCOUNT_SMTP_PASSWORD` 提供，不写示例、不复制到聊天。当前实现支持强制 STARTTLS；隐式 TLS 465 不在本阶段验收范围。错误日志只记录固定错误码，不记录服务商响应、邮箱、链接、正文或凭据。

密钥文件创建权限为 0600，目录为 0700；Windows 的实际访问控制由继承 NTFS ACL 决定，不能把 Unix mode 当作 Windows ACL 隔离。本机仅放合成数据；生产权限、磁盘加密、密钥备份和 Caddy 日志策略须另验收。

提供的 `config.example.json` 仅用于理解字段，不能直接启动：没有有效 key_file 时账户安全失败。运行 init-dev 可创建本地可用配置，不打印密钥。
