# 真实 SMTP 与自建邮件待办

本文件记录后续接入真实域名邮箱的选项和执行顺序。当前用户已决定先只验收本地合成环境，真实 SMTP 暂不联调。

记录日期：2026-10-02。服务商列表仅为候选，未逐项核实当前价格、额度、地区可用性或 SMTP 授权条件；正式选型时按服务商官方文档和控制台复核。

## 当前代码接口

StarAccount 已实现 SMTP 发信适配器，配置字段如下：

```json
{
  "mode": "test",
  "address": "127.0.0.1:4101",
  "origin": "http://127.0.0.1:4101",
  "database": ".local/smtp-test.db",
  "key_file": ".local/development.keys",
  "mail_mode": "smtp",
  "smtp_host": "服务商主机名",
  "smtp_port": 587,
  "mail_from": "account@example.com"
}
```

SMTP 用户名和密码只从进程环境变量读取：

```text
STARACCOUNT_SMTP_USER
STARACCOUNT_SMTP_PASSWORD
```

当前实现强制 STARTTLS，适合常见的 `587 + STARTTLS`。隐式 TLS `465`、API Key 发信和 OAuth2 SMTP 尚未接入。先向服务商确认端口、加密方式、认证方式和每日额度，不要猜 SMTP 主机名。

部分服务商把 API Key 作为 SMTP 密码使用，这种方式可能兼容当前适配器，应按其认证说明判断；通过 HTTP API 发信则需要另写适配器。上述配置复用开发密钥仅供本机测试，独立测试部署和正式部署应各自生成密钥。程序不会自动加载 `.env`，已经运行的进程也不会自动获得后来设置的环境变量。

## 可选服务

| 方案 | 适用情况 | 需要注意 |
|---|---|---|
| Amazon SES | 长期低成本事务邮件 | 需要域名验证，初始可能处于沙盒，需处理退信和生产额度 |
| Mailgun | 有投递日志和 SMTP Relay | 域名验证和套餐限制 |
| Postmark | 事务邮件投递质量优先 | 成本相对高，偏事务邮件 |
| Brevo | 小规模快速接入 | 需要核对 SMTP Relay 限额和发件人验证 |
| SendGrid | 成熟的 SMTP/API 平台 | 配置和账号审核较多 |
| Resend | 开发体验简洁 | 先确认 SMTP 能否满足当前适配器 |
| ZeptoMail | 验证邮件和通知邮件 | 核对地区、额度和域名验证要求 |
| 阿里云 DirectMail | 国内域名和服务器 | 以当前控制台支持的 SMTP/API 方式为准 |
| 腾讯云 SES | 国内部署 | 核对地域和发信权限 |
| Microsoft 365 / Google Workspace / Zoho Mail | 同时需要完整人工域名邮箱 | 事务邮件额度、异常登录和 SMTP 授权策略可能更严格 |
| 腾讯企业邮箱、阿里云企业邮箱 | 国内人工邮箱和少量自动邮件 | 不建议直接承担大量事务邮件，先核对限制 |

推荐顺序：先使用事务邮件服务做测试和首批运行；如果还需要人工收发，再单独使用域名邮箱托管。StarAccount 不要求同时运行一个完整收件箱。

## 自建选项

可以自建完整邮件服务器，常用方案包括：

- Mailcow：功能完整，包含 Postfix、Dovecot、Rspamd 和管理界面；
- Mailu：相对轻量，适合 Docker；
- docker-mailserver：配置型方案，适合熟悉 Docker 的维护者；
- Postfix + Dovecot + Rspamd：最灵活，维护成本最高。

自建至少需要固定公网 IP、DNS 管理权限、反向 DNS/PTR、TLS 证书、退信处理、备份、暴力破解防护和监控。域名通常需要配置 MX、A/AAAA、SPF、DKIM、DMARC，并保证服务器主机名、PTR 和证书一致。云服务器常默认限制 25 端口，新 IP 也可能没有投递信誉。

自建的主要风险是投递率和持续运维。服务能发出 SMTP 会话，不代表 Gmail、Outlook 等收件箱会接受。较稳妥的混合方案是：

```text
StarAccount -> 自建 Mailcow/Mailu -> 外部 SMTP Relay -> 收件人
```

如果只是 StarAccount 发信，可以直接连接外部 SMTP Relay，无需增加完整自建邮箱。已有域名邮箱接入事务邮件通常不必修改收信用的 MX；服务商要求的独立退信子域记录按其指引设置，避免影响现有邮箱。

不要把 SMTP 密码、授权码、API Key、DKIM 私钥或生产日志放进聊天、Git、配置样例或测试报告。

## 接入步骤

1. 选择服务商并创建专用发件地址，例如 `account@你的域名`。
2. 按服务商要求配置 SPF、DKIM、DMARC；不要重复添加 SPF 记录。
3. 创建 SMTP 专用密码或授权码，确认 `587 + STARTTLS`。
4. 在 StarAccount 使用独立的 `.local/smtp-test.json`、端口 `4101` 和数据库。
5. 在启动进程的终端设置 `STARACCOUNT_SMTP_USER` 和 `STARACCOUNT_SMTP_PASSWORD`。
6. 用自己控制的真实收件箱测试注册、验证、找回、过期链接、重复链接和旧会话撤销。
7. 检查服务商投递日志、退信、垃圾箱和延迟；测试失败时保留 `permanent_failure` 记录。
8. 真实域名和 HTTPS 准备好后，再将链接地址从回环地址迁移到测试域名；不要把 `127.0.0.1` 的验证链接发给真实用户。

真实 SMTP 配置完成前，StarAccount 页面继续显示“未联调”，不开放真实用户测试。

## 与后续阶段的关系

下一阶段是执行报告的 S03“标准单点登录与账户状态”：连接星图和账户的 OAuth/OIDC 登录，补会话查看与撤销、敏感操作重新认证、停用和注销恢复状态接口。本地继续使用合成邮件与测试身份，可以开发和验证标准登录协议。

真实 SMTP、测试域名、HTTPS 和真实账户全流程验收在 S04 集中处理。延后邮件选型不会阻止 S03 的本地开发，但不能据此把真实联调或账户基础版标记为完成。
