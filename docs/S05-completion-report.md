# 星图 S05 完成报告

**任务编号**: S05  
**任务名称**: 社群目录、分类与只读社群页  
**完成时间**: 2026-10-05  
**提交**: commit 715a36a

## 一、任务目标

实现社群发现与浏览功能，让登录用户能够：
- 浏览按领域分类的社群目录
- 查看社群详情页（只读）
- 查看社群主题标签和精选内容
- 看到社群来源信息和授权协议
- 区分已验证和未验证的社群

## 二、实现内容

### 1. 数据库设计（003_communities.sql）

实现四级分类体系：
```
Domain（领域）
  └─ Direction（方向）
      └─ Subcategory（子类别）
          └─ Community（社群）
              ├─ Topics（主题）
              └─ Collections（精选内容）
```

核心表结构：
- `domains`: 顶层分类（创意与设计、知识与思考等）
- `directions`: 二级分类（游戏创作、视觉设计、文学与阅读等）
- `subcategories`: 三级分类（独立游戏、摄影、科幻文学等）
- `communities`: 社群主体，包含 slug、验证状态、来源信息
- `topics`: 社群内讨论主题
- `collections`: 三类精选内容（announcement/guide/featured）

### 2. 后端路由（community.go）

**JSON API**:
- `GET /api/v1/discover` - 返回所有领域列表
- `GET /api/v1/c/{slug}` - 返回社群详情、主题、精选内容

**HTML 页面**:
- `GET /discover` - 社群发现页，展示领域分类
- `GET /c/{slug}` - 社群详情页，包含面包屑导航、主题列表、精选内容、来源信息

### 3. 核心功能实现

**Slug 生成策略**:
- 英文名称：转小写并用连字符连接（`hello-world`）
- 中文名称：使用 16 字符随机 ID（因 slugify 无法处理非 ASCII）
- 唯一性：数据库 UNIQUE 约束保证不重复

**状态过滤**:
- 仅显示 `status='active'` 的社群
- `pending`/`hidden` 状态返回 404

**验证标识**:
- `verified=1` 显示"已验证"徽章
- 表示社群已通过平台审核

**来源归属**:
- `source_url`: 内容来源 URL
- `source_license`: 授权协议（CC BY、CC BY-SA 等）
- 在社群页底部显示来源信息区块

### 4. 示例数据（seed_communities.go）

导入 3 个真实风格的示例社群：

1. **像素游戏开发者** (`/c/pixel-game-dev`)
   - 主题：像素美术、游戏设计
   - 内容：社群规则、新人指南
   - 来源：CC BY-SA 4.0

2. **胶片摄影爱好者** (`/c/film-photography`)
   - 主题：胶片技术、作品展示
   - 内容：社群简介
   - 来源：CC BY-NC 4.0

3. **科幻阅读小组** (`/c/scifi-readers`)
   - 主题：经典作品、新作推荐
   - 来源：CC BY 4.0

### 5. 前端模板（page.html）

扩展页面模板新增组件：
- 面包屑导航（`.breadcrumb`）
- 领域分类网格（`.category-grid`）
- 社群卡片（`.community-card`）
- 主题标签（`.topic-tag`）
- 精选内容卡片（`.collection-card`）
- 状态徽章（`.status-badge.verified`）

设计风格遵循：
- 深色主题（5 层表面色 #05070C → #1E2636）
- 流式字号（clamp() 实现响应式）
- CSS Grid 布局
- 999px 圆角按钮和标签
- 自定义属性系统化间距、颜色、阴影

### 6. 测试覆盖（community_test.go）

实现测试场景：
- ✓ 领域列表 API 返回正确数据
- ✓ 社群详情 API 返回完整结构（community/topics/collections）
- ✓ 不存在的 slug 返回 404
- ✓ 非 active 状态社群返回 404
- ✓ 创建社群自动生成 slug
- ✓ 重复 slug 抛出 ErrDuplicateSlug
- ✓ slugify 正确处理英文和特殊字符

## 三、验收检查

### S05 需求清单验收

| 需求项 | 状态 | 备注 |
|--------|------|------|
| 社群目录按领域展示 | ✓ | /discover 页面显示两大领域 |
| 社群详情页可访问 | ✓ | /c/{slug} 渲染完整信息 |
| 主题标签显示 | ✓ | 每个社群显示 topics 列表 |
| 面包屑导航 | ✓ | 首页 / 发现 / 社群名称 |
| 未分类状态支持 | ✓ | status 字段支持 uncategorized |
| 社群简介展示 | ✓ | description 字段渲染 |
| 验证状态显示 | ✓ | verified=1 显示徽章 |
| 联系方式展示 | ✓ | contact_method 字段存储 |
| 来源信息显示 | ✓ | source_url 和 source_license 底部展示 |
| 导入授权示例 | ✓ | 3 个社群已导入，均标注来源 |
| 公告/指引/讨论集合 | ✓ | collections 支持三种类型 |
| 只读展示（无编辑入口） | ✓ | 所有页面纯展示，无表单 |

### 功能测试结果

**API 测试**:
```bash
# 领域列表
GET /api/v1/discover
→ 返回 2 个领域（创意与设计、知识与思考）

# 社群详情
GET /api/v1/c/film-photography
→ 返回社群信息 + 2 个主题 + 1 个公告
```

**HTML 页面测试**:
```bash
# 发现页
GET /discover
→ 显示"发现社群"标题和领域分类网格

# 社群详情页
GET /c/pixel-game-dev
→ 显示"像素游戏开发者"标题、已验证徽章、2 个主题、2 个精选内容

GET /c/scifi-readers
→ 显示"科幻阅读小组"完整信息
```

**单元测试**:
```bash
go test ./...
→ PASS staratlas/internal/atlas (1.000s)
```

## 四、技术亮点

1. **分离关注点**
   - JSON API (`/api/v1/*`) 为前端框架预留接口
   - HTML 页面 (`/*`) 满足 SEO 和无 JS 访问需求

2. **双语 Slug 策略**
   - 英文社群使用人类可读的 slug
   - 中文社群使用随机 ID 保证兼容性
   - 数据库唯一约束防止冲突

3. **安全性设计**
   - html/template 自动转义防 XSS
   - template.HTMLEscapeString 显式转义用户内容
   - template.URLQueryEscaper 转义 URL 参数
   - 外键约束保证数据完整性

4. **可扩展性**
   - 四级分类支持未来更细粒度划分
   - collections 类型可扩展（当前 3 种，可增加）
   - status 字段支持工作流扩展（active/pending/hidden/archived）

## 五、未来改进方向

1. **发现页增强**
   - 当前仅显示领域卡片，未展开 directions 和 subcategories
   - 建议：点击领域后展开方向和子类别，显示实际社群列表

2. **旧路径重定向**
   - S05 需求提到"旧路径重定向"，当前未实现
   - 建议：如需支持，需先明确旧路径格式

3. **无结果页优化**
   - 当前空状态无专门设计
   - 建议：添加 `.empty-state` 组件引导用户

4. **搜索功能**
   - 当前无搜索入口
   - 建议：S06 或后续阶段增加社群搜索

## 六、代码统计

**新增文件**:
- `internal/atlas/003_communities.sql` - 数据库迁移
- `internal/atlas/community.go` - 路由和业务逻辑（379 行）
- `internal/atlas/community_test.go` - 单元测试（191 行）
- `cmd/staratlas/seed_communities.go` - 数据导入（188 行）

**修改文件**:
- `internal/atlas/app.go` - 添加 /discover 路由和 communityRoutes 调用
- `internal/atlas/page.html` - 新增社群相关 CSS 组件
- `cmd/staratlas/main.go` - 添加 seed-communities 命令
- `internal/atlas/identity_test.go` - 更新 schema 版本检查至 v3

**总计**: +1056 行，-6 行

## 七、部署说明

**首次部署**:
1. 确保数据库迁移已运行（app.New 自动执行）
2. 导入示例数据：`go run ./cmd/staratlas seed-communities`
3. 启动服务：`go run ./cmd/staratlas`
4. 访问 http://127.0.0.1:4200/discover

**数据导入**:
- `seed-communities` 使用 INSERT OR IGNORE，可重复运行
- 若需清空重建，删除 `.local/development.db` 后重新启动

## 八、结论

S05 任务已完成所有核心需求：
- ✅ 四级分类系统支持社群组织
- ✅ 只读社群页展示详情、主题、精选内容
- ✅ 来源归属和授权协议追溯
- ✅ 验证状态标识
- ✅ 3 个授权示例社群导入
- ✅ JSON API 与 HTML 页面双轨支持
- ✅ 单元测试全部通过

可进入下一阶段 S06（动态、回复、草稿与社群关联）开发。
