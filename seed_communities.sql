-- 种子数据：领域、方向、子类别

-- 领域 (Domains)
INSERT INTO domains (id, name, description, display_order, created_at) VALUES
('tech', '技术', '软件开发、工程、数据科学', 1, unixepoch()),
('creative', '创意', '设计、艺术、写作、音乐', 2, unixepoch()),
('biz', '商业', '创业、产品、营销、职业发展', 3, unixepoch());

-- 方向 (Directions)
INSERT INTO directions (id, domain_id, name, description, display_order, created_at) VALUES
('web', 'tech', 'Web 开发', '前端、后端、全栈', 1, unixepoch()),
('data', 'tech', '数据科学', '机器学习、AI、数据分析', 2, unixepoch()),
('design', 'creative', '设计', 'UI/UX、平面设计、品牌', 1, unixepoch()),
('writing', 'creative', '写作', '内容创作、文案、博客', 2, unixepoch()),
('product', 'biz', '产品', '产品管理、用户研究', 1, unixepoch());

-- 子类别 (Subcategories)
INSERT INTO subcategories (id, direction_id, name, description, display_order, created_at) VALUES
('frontend', 'web', '前端开发', 'React、Vue、CSS', 1, unixepoch()),
('backend', 'web', '后端开发', 'Go、Python、数据库', 2, unixepoch()),
('ml', 'data', '机器学习', '深度学习、模型训练', 1, unixepoch()),
('ui', 'design', 'UI 设计', '界面设计、组件库', 1, unixepoch()),
('content', 'writing', '内容创作', '博客、文章、教程', 1, unixepoch()),
('pm', 'product', '产品管理', '需求、路线图、协作', 1, unixepoch());

-- 社群 (Communities)
INSERT INTO communities (id, slug, name, description, subcategory_id, status, contact_method, source_url, source_license, verified, created_at, updated_at) VALUES
('react-cn', 'react-cn', 'React 中文社区', '讨论 React 开发技巧、最佳实践和生态工具', 'frontend', 'active', '官方论坛', 'https://react.dev', 'CC BY 4.0', 1, unixepoch(), unixepoch()),
('vue-cn', 'vue-cn', 'Vue.js 中文社区', 'Vue 3、Composition API、Nuxt 等相关讨论', 'frontend', 'active', '官方论坛', 'https://vuejs.org', 'CC BY 4.0', 1, unixepoch(), unixepoch()),
('golang', 'golang', 'Go 语言社区', 'Go 开发、并发编程、微服务架构', 'backend', 'active', '官方论坛', 'https://go.dev', 'CC BY 4.0', 1, unixepoch(), unixepoch()),
('pytorch', 'pytorch', 'PyTorch 中文社区', '深度学习、神经网络、模型优化', 'ml', 'active', 'Discord', 'https://pytorch.org', 'CC BY 4.0', 1, unixepoch(), unixepoch()),
('figma-cn', 'figma-cn', 'Figma 设计师社区', 'UI 设计、原型、组件系统分享', 'ui', 'active', 'Slack', 'https://figma.com', 'CC BY 4.0', 1, unixepoch(), unixepoch()),
('indie-cn', 'indie-cn', '独立开发者', '独立产品、变现、技术栈选择', 'pm', 'active', 'Telegram', '', '', 0, unixepoch(), unixepoch());

-- 主题 (Topics)
INSERT INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES
('react-hooks', 'react-cn', 'hooks', 'Hooks', 'useState、useEffect、自定义 Hooks', 1, 'active', unixepoch()),
('react-perf', 'react-cn', 'performance', '性能优化', 'memo、useMemo、代码分割', 2, 'active', unixepoch()),
('vue-comp', 'vue-cn', 'composition', 'Composition API', 'setup、ref、reactive', 1, 'active', unixepoch()),
('go-concurrency', 'golang', 'concurrency', '并发编程', 'goroutine、channel、sync', 1, 'active', unixepoch()),
('pytorch-train', 'pytorch', 'training', '模型训练', 'DataLoader、优化器、调参', 1, 'active', unixepoch()),
('figma-plugins', 'figma-cn', 'plugins', '插件开发', 'Figma API、自动化工作流', 1, 'active', unixepoch());

-- 公告和指南 (Collections)
INSERT INTO collections (id, community_id, type, title, slug, description, content, display_order, status, created_at, updated_at) VALUES
('react-welcome', 'react-cn', 'announcement', '欢迎来到 React 中文社区', 'welcome', '社区指南和行为准则', '欢迎加入 React 中文社区！这里是讨论 React 开发的地方。

请遵守以下规则：
- 保持友善和尊重
- 提问前先搜索已有讨论
- 分享代码时使用代码块
- 标注相关主题标签

祝你在这里学到有用的知识！', 1, 'published', unixepoch(), unixepoch()),

('golang-guide', 'golang', 'guide', 'Go 开发最佳实践', 'best-practices', '编码规范和常见模式', '# Go 开发最佳实践

## 项目结构
推荐使用标准的项目布局：
- cmd/ 存放主程序入口
- internal/ 存放私有代码
- pkg/ 存放可复用的公共库

## 错误处理
- 总是检查错误返回值
- 使用 errors.Is 和 errors.As
- 为错误添加上下文信息

## 并发
- 优先使用 channel 通信
- 避免共享内存
- 使用 context 控制超时和取消', 1, 'published', unixepoch(), unixepoch()),

('indie-resources', 'indie-cn', 'guide', '独立开发者资源清单', 'resources', '工具、平台、学习资源', '# 独立开发者资源

## 开发工具
- Next.js - 全栈框架
- Supabase - 后端服务
- Tailwind CSS - 样式方案

## 部署平台
- Vercel - 前端托管
- Fly.io - 全栈应用
- Railway - 数据库和服务

## 支付集成
- Stripe - 国际支付
- Lemon Squeezy - 商家服务

## 营销推广
- Product Hunt - 产品发布
- Twitter/X - 社交媒体
- Indie Hackers - 社区交流', 1, 'published', unixepoch(), unixepoch());
