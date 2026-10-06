package main

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

func seedCommunities(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()

	// Domain: 创意与设计
	domainID := "creative-design"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO domains (id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?)`,
		domainID, "创意与设计", "从艺术创作到实用设计的社群集合", 1, now); err != nil {
		return err
	}

	// Direction: 游戏创作
	directionID := "game-dev"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO directions (id, domain_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		directionID, domainID, "游戏创作", "独立游戏开发、游戏设计和互动体验", 1, now); err != nil {
		return err
	}

	// Subcategory: 独立游戏
	subcategoryID := "indie-games"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO subcategories (id, direction_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		subcategoryID, directionID, "独立游戏", "小团队或个人开发的独立游戏作品", 1, now); err != nil {
		return err
	}

	// Community 1: 像素游戏开发者
	communityID1 := "pixel-game-dev"
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO communities (id, slug, name, description, subcategory_id, status, contact_method, source_url, source_license, verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, communityID1, "pixel-game-dev", "像素游戏开发者（合成示例）",
		"专注于像素艺术风格游戏的创作社群，分享开发经验、美术资源和游戏设计思路。",
		subcategoryID, "active", "本地测试反馈，请联系项目负责人", "https://example.com/pixel-games", "synthetic / 本地演示，未获得真实社群授权", 0, now, now); err != nil {
		return err
	}

	// Topics for Community 1
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-pixel-art", communityID1, "pixel-art", "像素美术", "像素艺术技巧、调色板和动画制作", 1, "active", now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-game-design", communityID1, "game-design", "游戏设计", "关卡设计、玩法机制和平衡性讨论", 2, "active", now); err != nil {
		return err
	}

	// Collection: Announcement
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO collections (id, community_id, type, title, slug, description, content, display_order, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "coll-welcome", communityID1, "announcement", "欢迎来到像素游戏开发者社群", "welcome",
		"社群介绍和基本规则",
		"欢迎各位像素游戏爱好者！这里是一个分享创作经验、展示作品和互相学习的地方。\n\n请遵守以下基本规则：\n1. 尊重他人的创作和意见\n2. 分享时注明原创或来源\n3. 保持友善和建设性的讨论氛围",
		1, "published", now, now); err != nil {
		return err
	}

	// Collection: Guide
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO collections (id, community_id, type, title, slug, description, content, display_order, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "coll-guide", communityID1, "guide", "新人入门指南", "beginner-guide",
		"帮助新手快速开始像素游戏开发",
		"# 新手入门\n\n## 推荐工具\n- Aseprite: 专业的像素艺术编辑器\n- Godot/Unity: 适合独立开发的游戏引擎\n\n## 学习资源\n- Pixel Art Tutorial（像素艺术教程）\n- Game Design Patterns（游戏设计模式）\n\n## 社群活动\n每月主题创作挑战，欢迎参与！",
		2, "published", now, now); err != nil {
		return err
	}

	// Direction: 视觉设计
	directionID2 := "visual-design"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO directions (id, domain_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		directionID2, domainID, "视觉设计", "平面设计、UI/UX 和品牌视觉", 2, now); err != nil {
		return err
	}

	// Subcategory: 摄影
	subcategoryID2 := "photography"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO subcategories (id, direction_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		subcategoryID2, directionID2, "摄影", "摄影技术、后期处理和作品分享", 1, now); err != nil {
		return err
	}

	// Community 2: 胶片摄影爱好者
	communityID2 := "film-photography"
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO communities (id, slug, name, description, subcategory_id, status, contact_method, source_url, source_license, verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, communityID2, "film-photography", "胶片摄影爱好者",
		"分享胶片摄影作品、冲洗经验和相机使用心得的社群。保留摄影的温度和质感。",
		subcategoryID2, "active", "本地合成示例，请联系项目负责人", "https://example.com/film-photo", "synthetic / 本地演示，未获得真实社群授权", 0, now, now); err != nil {
		return err
	}

	// Topics for Community 2
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-film-tech", communityID2, "film-tech", "胶片技术", "胶卷选择、曝光技巧和冲洗经验", 1, "active", now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-works", communityID2, "works", "作品展示", "分享你的胶片摄影作品", 2, "active", now); err != nil {
		return err
	}

	// Collection for Community 2
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO collections (id, community_id, type, title, slug, description, content, display_order, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "coll-film-welcome", communityID2, "announcement", "关于本社群", "about",
		"胶片摄影社群简介",
		"这里聚集了热爱胶片摄影的朋友们。无论你是刚接触胶片的新手，还是有多年经验的老手，都欢迎在这里分享交流。\n\n我们相信，胶片摄影不仅是一种技术，更是一种对摄影本质的回归。",
		1, "published", now, now); err != nil {
		return err
	}

	// Domain 2: 知识与思考
	domainID3 := "knowledge-thinking"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO domains (id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?)`,
		domainID3, "知识与思考", "学术讨论、读书分享和深度思考", 2, now); err != nil {
		return err
	}

	// Direction: 文学与阅读
	directionID3 := "literature"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO directions (id, domain_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		directionID3, domainID3, "文学与阅读", "文学作品讨论和阅读心得分享", 1, now); err != nil {
		return err
	}

	// Subcategory: 科幻文学
	subcategoryID3 := "scifi"
	if _, err := tx.Exec(`INSERT OR IGNORE INTO subcategories (id, direction_id, name, description, display_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		subcategoryID3, directionID3, "科幻文学", "科幻小说、科幻理论和未来想象", 1, now); err != nil {
		return err
	}

	// Community 3: 科幻阅读小组
	communityID3 := "scifi-readers"
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO communities (id, slug, name, description, subcategory_id, status, contact_method, source_url, source_license, verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, communityID3, "scifi-readers", "科幻阅读小组",
		"一起阅读和讨论科幻作品，探索科技与人性、现实与想象的边界。",
		subcategoryID3, "active", "本地合成示例，请联系项目负责人", "https://example.com/scifi", "synthetic / 本地演示，未获得真实社群授权", 0, now, now); err != nil {
		return err
	}

	// Topics for Community 3
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-classics", communityID3, "classics", "经典作品", "讨论经典科幻小说", 1, "active", now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topics (id, community_id, slug, name, description, display_order, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"topic-new-releases", communityID3, "new-releases", "新作推荐", "分享和讨论新出版的科幻作品", 2, "active", now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Println("✓ 已导入 3 个示例社群")
	log.Println("  - 像素游戏开发者 (/c/pixel-game-dev)")
	log.Println("  - 胶片摄影爱好者 (/c/film-photography)")
	log.Println("  - 科幻阅读小组 (/c/scifi-readers)")
	return nil
}
