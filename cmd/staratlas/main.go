package main

import (
	"context"
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
	app, err := atlas.New(config)
	if err != nil {
		logger.Fatal("启动失败：数据库或迁移不可用")
	}
	defer app.Close()
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
	logger.Print("本机内部测试服务启动：", config.Origin, "；新星账户未联调")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Print("监听失败")
	}
	cancel()
	<-stopped
	worker.Wait()
}
