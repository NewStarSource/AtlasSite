---
title: SA5-星图 Wiki 条目模板
version: 2.1
created: 2026-08-24
status: confirmed
updated: 2026-09-05
owner: 新星团队
reviewers: 用户（本会话审议确认）
last_reviewed: 2026-09-05
effective_date: 2026-09-05
supersedes: 2.0-draft
previous_status: draft
license: CC BY-SA 4.0
---

# 星图 Wiki 条目模板 v2.1

本文档提供六类首批正式条目的基础模板。基础字段由新星团队维护；相关社群可以在不改变基础字段、不突破隐私和安全底线的前提下增加扩展字段。

所有可验证事实必须使用脚注。成员自述、观点和解释性文字可以不使用脚注，但必须明确其性质。

## 1. 社群条目

```markdown
---
type: community
title: 社群名称
status: active
source: https://example.com/source
first_verified_at: 2026-08-24
---

# 社群名称

## 它是什么

## 主要讨论或创作什么

## 当前状态

## 如何加入或接触

## 大家共同在做什么

## 历史与时间线

## 代表性讨论与创作

## 成员自述

## 不同说法

## 关联主题与社群

## 公开规则与代表

## 来源
```

## 2. 作品条目

```markdown
---
type: work
title: 作品名称
status: public
source: https://example.com/source
---

# 作品名称

## 基本信息

## 简介

## 创作者与制作人员

## 发布时间与版本

## 公开展示方式

## 版权与授权说明

## 关联社群、人物与事件

## 相关讨论与创作

## 来源
```

未公开作品须经作者主动授权建立，默认仅作者及指定审核人员可见；扩展到指定社群成员或全体登录用户须另行确认，并遵守作者的隐藏、删除或匿名化请求。

## 3. 梗条目

```markdown
---
type: meme
title: 梗名称
source: https://example.com/source
---

# 梗名称

## 基本含义

## 出现时间

## 传播方式

## 使用范围

## 典型用法

## 变体与演变

## 关联社群、作品与事件

## 不同说法

## 来源
```

## 4. 黑话条目

```markdown
---
type: jargon
title: 黑话
source: https://example.com/source
---

# 黑话

## 词义

## 使用范围

## 词源与出现时间

## 演变

## 例句与典型用法

## 不同社群中的差异

## 相关梗与社群

## 来源
```

## 5. 人物条目

人物必须以公开身份参与互联网文化，并与社群有明确关联。普通成员、匿名参与者和仅因被讨论而出现的人，不自动建立人物条目。

```markdown
---
type: person
title: 公开身份或署名
related_community: 社群名称
source: https://example.com/source
---

# 公开身份或署名

## 公开身份

## 与社群的关联

## 与互联网文化直接相关的公开事实

## 本人自述

## 争议与不同说法

## 相关作品与事件

## 隐私与风险说明

## 来源
```

## 6. 事件条目

```markdown
---
type: event
title: 事件名称
date_start: 2026-08-24
date_end:
source: https://example.com/source
---

# 事件名称

## 事件概述

## 发生时间

## 相关社群与对象

## 事件经过

## 影响

## 不同说法

## 相关作品、梗与人物

## 风险与隐私说明

## 来源
```

## 7. 通用名称字段

```yaml
names:
  primary: 中性主标题
  chinese: 中文名
  original: 原文名
  english: English name
  community_names:
    - 社群内部称呼
  historical_names:
    - 历史旧称
  redirects:
    - 常见别名
```

同名不同对象必须使用独立消歧页面，不得仅凭名称合并。

## 8. 来源脚注示例

```markdown
这个事实有公开来源。[^1]

[^1]: [来源标题](https://example.com/source)，访问日期：2026-08-24。
```

私下核验材料使用：

```markdown
该事实经私下核验。[^private-1]

[^private-1]: 私下核验，来源受限。原始材料仅限团队指定核验人员查看，核验完成后 7 天删除；代表查看必要脱敏结论。
```

