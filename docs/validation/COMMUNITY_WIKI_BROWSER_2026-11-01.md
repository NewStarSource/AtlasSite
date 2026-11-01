# 社群工具与 Wiki 页面验收

本批使用临时双服务、由真实 OIDC 流程建立的合成账号及 Chromium 页面操作。预置团队审核员、限定社群代表和普通作者，另有匿名访问者；权限登记只作用于临时社群，不代表真实社群认证或实际人员试用。Wiki 仅在临时配置开启，现有服务和服务器未修改。

## 浏览器覆盖

四组为 1440px/390px，各自开启或关闭 JavaScript，逐组运行完整流程。所有保存按钮以聚焦后 Enter 提交，保持原生表单与服务端权限检查。结果保存为[执行结果](COMMUNITY_WIKI_BROWSER_2026-11-01.json)。

- 社群代表通过真实页面维护公告、新人指引和讨论合集，完成草稿、发布、版本查看及撤下；匿名访问者不能看到草稿或撤下内容，合集显示原帖引用。
- 普通作者创建发现页，第二位参与者修改，旧表单触发冲突并保留输入。
- 社群和已公开作品两类正式条目实际填写、私有提交、团队审核发布。匿名无法读取待审页，代表也不能读取他人的私有提交。
- 公开建议修改实际采纳生成版本；错误脚注返回可修改表单并保留输入；完整修订附理由退回后重新打开本人原稿，正文保留；历史可读取已发布版本。
- 多层讨论通过根讨论和下游回复表单实际提交，匿名可读；本人撤回根讨论后，下游不再展示。

代表、作者与团队保持不同身份。测试没有强制点击、直接修改页面表单状态或绕过审核权限。定位、输入和提交均作用于真实页面；测量页面宽度与控件位置使用浏览器自动化接口，不需要产品 JavaScript。

## 发现并修复

最初的 390px 无 JavaScript 用例在确认建议审核时失败：移动底栏截获确认框的点击。新增移动视口滚动留白及主内容控件滚动边距，原点击重新通过。另断言确认框整个高度位于底栏上方，避免只用脚本勾选来掩盖遮挡。其余界面结构、权限和数据模型未改变。

使用本地构建 `atlas-ui-20261101.exe`，SHA-256 为 `82fe5c51fc1bfbc4e642c0a11f7fe5c0c0b920506647f95d75e8efbad7285579`。构建元数据基线为星图 `6d92a3b31796033e93d9a6fd357b1d06fb1da100`，modified=true，包含本批滚动样式修复；账户使用此前候选中的原生程序。它不是上批 79cf2b3 候选的同一星图二进制，正式发布仍需从提交后的干净源码重建。

## Linux 完整检查补充

本机原 IPv4 回环路由仍异常，独立 socket 探测失败。没有修改主机路由、代理、防火墙或安全参数。使用 `unshare --user --map-root-user --net` 创建一次性用户与网络命名空间，仅在该空间启动 lo 回环。

在其中运行原有完整 `go test -race ./... -count=1 -timeout 900s`，没有筛选用例，星图完整通过，194.485 秒。工具链为 Go 1.27.3 linux/amd64，依赖来自已缓存模块；没有降低密码强度或改变身份服务不可用行为。

同一隔离方式运行原有完整 IPv4 生产模式 HTTPS/OIDC 联调通过，含临时可信 CA、公开阅读、邀请码注册、私有页面隔离、Secure Cookie、发布、恢复码重放拒绝和跨服务撤销。原先失败记录保留，隔离验证成功不等于主机网络已经修好。

复现入口为 `scripts/with_isolated_network.py`，需在支持无特权用户命名空间的 Linux/WSL 中使用，并事先准备依赖。它只在新建空间内启动 lo，拒绝在不支持时退回宿主网络。示例在星图仓库目录运行：

```bash
python3 scripts/with_isolated_network.py -- /path/to/go test -race ./... -count=1 -timeout 900s
```

新增浏览器回归已接入双服务发布工作流，实际远端运行结果尚未取得。本地脚本存在或本地通过不等于 CI 已经通过。

## 页面证据与剩余项

截图仅包含合成身份及公开来源样例，不含密码、恢复码或真实用户数据。

- [窄屏原生公告维护](screenshots/community-wiki/tool-announcement-form-390-native.png)
- [窄屏合集引用](screenshots/community-wiki/tool-discussion-public-390-native.png)
- [桌面社群条目](screenshots/community-wiki/wiki-community-public-1440-js.png)
- [窄屏作品模板](screenshots/community-wiki/wiki-work-form-390-native.png)
- [窄屏建议审核](screenshots/community-wiki/wiki-community-suggestion-review-390-native.png)
- [退回稿重提](screenshots/community-wiki/wiki-work-resubmit-390-native.png)
- [版本历史](screenshots/community-wiki/wiki-work-history-390-native.png)
- [窄屏多层讨论](screenshots/community-wiki/wiki-work-discussion-390-native.png)

本批是自动化页面与实际渲染检查，不替代真实参与者使用。实际多人试用记录、外部告警、异机备份与实际隔离恢复仍待完成，没有推送、部署、迁移生产库或重启服务器。
