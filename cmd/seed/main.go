package main

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: seed <database-path>")
	}

	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	seedSQL := `
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
`

	_, err = db.Exec(seedSQL)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("种子数据导入成功")
}
