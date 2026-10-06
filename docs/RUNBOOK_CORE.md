# 本地运行、维护与恢复

本文适用于当前回环 development/test 环境。先从 StarAccount 目录操作账户服务，再从 AtlasSite 目录操作星图。配置内相对路径按命令的工作目录解释。首次升级先停止旧进程，对数据库做停机副本；有 WAL 时不能只复制正在写入的主文件。

## 构建与检查

在 `D:/NewStarProject/AtlasSite` 执行：

```powershell
pwsh -File scripts/check_core.ps1
```

脚本构建两个仓库的 `bin/*.exe` 并自动执行 HTTP 集成。没有图形浏览器步骤。普通启动仍使用各仓库 README 的 init-dev、init-oidc 和 `-config` 命令。已有初始化文件不会覆盖。

## 新增文件边界

星图默认由 database 路径派生以下文件，可通过相应 Config 字段改为独立路径：

| 文件 | 用途 | 恢复要求 |
| --- | --- | --- |
| `.private.db`、`.vault.key` | 加密草稿、短期旧正文、隔离证据、图片独立密钥 | 单独保护，不能混进最长 12 个月的普通备份；私库缺失时不重新生成旧图片密钥或旧证据 |
| `.operations.jsonl`、`.anchors` | 内容删除、图片撤回、取消收藏/订阅、屏蔽、关系隐私和注销的独立清单 | 使用最新副本，不能随旧主库回滚；缺失、截断或校验失败会阻止恢复正常 |
| `.backups`、`.backup.key` | 加密普通快照和单独备份密钥 | 归档和密钥分开保存；归档带 JSON 清单与哈希 |
| `.csrf.key` | 未配置 OIDC 时的本地 CSRF 密钥 | 仅用于该实例，禁止复制为通用默认密钥 |

账户对应 `.security.jsonl`、`.anchors`、`.initialized`、`.backups` 和 `.backup.key`。账户 96 字节配置密钥、OIDC 客户端密钥和签名私钥也需独立保护。Windows 新增敏感文件使用当前用户、SYSTEM 和 Administrators 的受限 DACL；其他平台使用 0600/0700。正式服务账户需单独验证权限。

普通快照不包含会话、OIDC 授权请求、草稿正文或案件材料。星图已发布图片在主库中仍是密文，解密密钥只在独立私库。恢复图片和仍在保留期内的证据需要可用的独立私库及密钥；仅凭普通快照不能证明整个存储灾难可恢复。本地默认路径位于同一磁盘，正式离机保护和短期私库恢复需另行配置、演练。

## 备份与诊断

星图后台每小时生成普通加密快照，账户后台在分钟巡检中生成小时快照。成功后保留小时 48 小时、每日 30 天、每月 12 个月的档位。删除、限制和注销清理会清除已有普通归档中的相应原文；草稿 30 天、未发布上传 24 小时、普通通知 180 天，案件未结时冻结关联证据，结案后保留 30 天。清理由后台每日/每分钟任务执行，不承诺文件系统取证级擦除。

各仓库分别执行：

```powershell
./bin/staratlas.exe -config .local/development.json backup
./bin/staratlas.exe -config .local/development.json diagnose
# 从 StarAccount 目录执行
./bin/staraccount.exe -config .local/development.json backup
./bin/staraccount.exe -config .local/development.json diagnose
```

诊断检查完整性、清单和备份哈希/解密有效性。星图另检查失败任务、积压、超时案件及 WAL 文件增长。独立脚本检查两个 HTTP 健康入口及磁盘空间；输出不含内容、密码、token 或证据：

```powershell
pwsh -File scripts/watchdog.ps1
pwsh -File scripts/watchdog.ps1 -TestAlert
```

告警保存在 `.local/watchdog/alerts.jsonl`。状态不变时保持安静，变化或恢复时记录新事件；有问题退出码为 1。注册独立计划任务及外部接收渠道之前，不能称已完成全天告警覆盖。脚本不会自行注册任务或发送邮件。

## 维护与单人离线限制

```powershell
./bin/staratlas.exe -config .local/development.json maintenance readonly
./bin/staratlas.exe -config .local/development.json maintenance isolated
./bin/staratlas.exe -config .local/development.json maintenance normal
```

normal 会重新验证和重放清单。readonly 保留读取、举报、紧急请求、退出及身份可靠时的本人导出/注销；普通投稿互动暂停。isolated 进一步关闭导出。数据库不可用时的求助覆盖仍依赖独立渠道，不能仅依靠同一个服务进程。

可在 Atlas 配置增加 `write_until`，使用包含时区的 RFC3339 时间，例如 `2026-10-06T15:00:00+08:00`。时间到后下一次写请求进入只读。未确认的紧急请求超过 30 分钟也会触发只读。重开前检查值守能力、未处理案件和诊断结果，不扩大邀请码发放。

## 举报处理

```powershell
./bin/staratlas.exe -config .local/development.json case-list
./bin/staratlas.exe -config .local/development.json case-evidence <case-uuid> .local/evidence-unique.json
./bin/staratlas.exe -config .local/development.json case-resolve <case-uuid> reviewer-a restricted
./bin/staratlas.exe -config .local/development.json case-resolve <appeal-uuid> reviewer-b released
```

证据命令只写新的受限本地文件，不输出正文，不覆盖已有文件。可用决定为 dismissed、restricted、released 和 needs-independent-review。申诉复核标识必须不同于原决定；释放只撤销父案件对应限制。冲突案件保持 waiting_independent_review，不能由同一处理人通过更换标识来宣称已经独立终审。原始证据禁止上传外部 AI 服务。

## 隔离恢复与回退

1. 暂停两个服务的用户写入，保护最新独立清单、锚点、密钥和私库，确认快照和 JSON 清单的来源。不要回滚这些独立文件。
2. 在原仓库目录执行 restore，目标必须是尚不存在的新路径：

```powershell
# StarAccount 目录
./bin/staraccount.exe -config .local/development.json restore <account.backup> .local/restore/account.db
# AtlasSite 目录
./bin/staratlas.exe -config .local/development.json restore <atlas.backup> .local/restore/atlas.db
```

3. 复制测试配置，将 database 指向隔离新库，origin/address 改用空闲回环端口。独立清单、账户配置密钥、星图私库和密钥必须仍指向最新独立文件，不能默认为新库生成空清单。账户恢复已清除会话、授权 code/token 和邮件 token；快照后发生凭据变更的账号进入 recovery_pending 并清空旧密码哈希，禁止用旧密码继续登录，后续由受控身份恢复流程处理。
4. 星图恢复库保持 readonly；核对删除内容、已撤回图片、屏蔽、关系隐藏和取消互动没有复活。运行 integrity 检查、身份失败场景及核心 HTTP 检查。仅进行读取验证时不要把隔离实例与活动主实例并行写入同一独立私库或清单。
5. 验证失败保持隔离。版本回退使用旧程序与匹配的迁移前副本，在隔离目录先验证；旧程序不得直接打开 schema 6/5 新库。恢复时若缺少最新撤回清单或可靠私库结果，不开放真实写入。

真实部署切换、灾难恢复、外部通知和用户资格放行不在本地合成测试中自动执行。
