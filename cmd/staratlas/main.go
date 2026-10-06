package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"staratlas/internal/atlas"
)

func main() {
	logger := log.New(os.Stdout, "staratlas ", log.LstdFlags|log.LUTC)
	if len(os.Args) > 1 && os.Args[1] == "init-oidc" {
		source := "../StarAccount/.local/oidc-client.secret"
		if len(os.Args) > 2 {
			source = os.Args[2]
		}
		if err := atlas.InitOIDC(source); err != nil {
			logger.Fatal("本地 OIDC 配置失败：", err)
		}
		logger.Print("本地 OIDC 配置已完成，未输出凭据")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "init-dev" {
		if err := atlas.InitDevelopment(); err != nil {
			logger.Fatal("初始化失败：", err)
		}
		logger.Print("本机配置已创建于 .local/development.json")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "seed-communities" {
		if err := seedCommunities(".local/development.db"); err != nil {
			logger.Fatal("导入社群数据失败：", err)
		}
		logger.Print("✓ 社群数据导入完成")
		return
	}
	filename := flag.String("config", ".local/development.json", "配置路径")
	flag.Parse()
	config, err := atlas.LoadConfig(*filename)
	if err != nil {
		logger.Fatal(err)
	}
	if len(flag.Args()) > 0 && flag.Args()[0] == "restore" {
		if len(flag.Args()) != 3 {
			logger.Fatal("restore 需要备份文件及新的隔离数据库路径")
		}
		if err = atlas.RestoreBackup(config, flag.Args()[1], flag.Args()[2]); err != nil {
			logger.Fatal("恢复失败：", err)
		}
		logger.Print("隔离恢复通过；新数据库保持只读，原数据库未修改")
		return
	}
	app, err := atlas.New(config)
	if err != nil {
		logger.Fatal("启动失败：数据库或迁移不可用")
	}
	defer app.Close()
	if len(flag.Args()) > 0 {
		args := flag.Args()
		var result any
		switch args[0] {
		case "backup":
			result, err = app.Backup()
		case "diagnose":
			result, err = app.Diagnostics()
		case "maintenance":
			if len(args) != 2 {
				logger.Fatal("maintenance 需要 normal/readonly/isolated")
			}
			err = app.SetMaintenance(args[1])
			result = map[string]string{"mode": args[1]}
		case "case-list":
			result, err = app.CaseList()
		case "case-evidence":
			if len(args) != 3 {
				logger.Fatal("case-evidence 需要案件 ID 及新的受限输出文件路径")
			}
			err = app.CaseEvidence(args[1], args[2])
			result = map[string]string{"status": "saved_locally"}
		case "case-resolve":
			if len(args) != 4 {
				logger.Fatal("case-resolve 需要案件 ID、复核人员标识及决定")
			}
			err = app.ResolveCase(args[1], args[2], args[3])
			result = map[string]string{"status": "recorded"}
		case "cleanup":
			err = app.CleanupCore()
			result = map[string]string{"status": "cleaned"}
		default:
			logger.Fatal("未知运行命令")
		}
		if err != nil {
			logger.Fatal("运行操作失败：", err)
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			logger.Fatal("输出失败")
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var worker sync.WaitGroup
	worker.Add(1)
	go func() { defer worker.Done(); app.Worker(ctx) }()
	server := atlas.Server(config, app.Handler())
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
		close(stopped)
	}()
	logger.Print("本机内部测试服务启动：", config.Origin, "；仅本地测试，真实部署尚未验收")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Print("监听失败")
	}
	cancel()
	<-stopped
	worker.Wait()
}
