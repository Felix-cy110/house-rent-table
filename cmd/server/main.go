package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/aiclient"
	"github.com/Felix-cy110/house-rent-table/internal/config"
	"github.com/Felix-cy110/house-rent-table/internal/httpapi"
	"github.com/Felix-cy110/house-rent-table/internal/persistence"
)

func main() {
	if err := config.LoadEnvFile(".env"); err != nil {
		log.Fatal(err)
	}
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "built frontend directory")
	template := flag.String("template", "", "optional XLSX template path")
	aiConfigDir := flag.String("ai-config-dir", "", "directory for this app's saved API connection")
	rentTolerance := flag.Int("rent-premium-tolerance", 20, "rent premium tolerance percent, 0 to 100 (a review hint, not a market standard)")
	queueDir := flag.String("persistence-dir", os.Getenv("RENT_PERSISTENCE_DIR"), "optional durable background queue directory")
	flag.Parse()
	if *rentTolerance < 0 || *rentTolerance > 100 {
		log.Fatal("租金溢价容忍值必须在 0 至 100 之间")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
		log.Fatal("本轮仅支持本机单用户运行，请使用 127.0.0.1 或 localhost 监听地址")
	}
	fallback, err := aiclient.NewSearchFallback(os.Getenv("RENT_SEARCH_PROVIDER"), os.Getenv("RENT_SEARCH_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	agent := aiclient.New(aiclient.Options{ConfigDir: *aiConfigDir, RentPremiumTolerance: rentTolerance, SearchFallback: fallback})
	var background *persistence.Worker
	var feedbackStore httpapi.BrokerFeedbackStore
	var defaultCurrency *string
	if dsn := os.Getenv("RENT_MYSQL_DSN"); dsn != "" {
		currency, provided := os.LookupEnv("RENT_DEFAULT_CURRENCY")
		if !provided {
			currency = "CNY"
		}
		if currency != "" {
			if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(currency) {
				log.Fatal("RENT_DEFAULT_CURRENCY 必须为空或三个大写字母")
			}
			defaultCurrency = &currency
		}
		store, err := persistence.OpenMySQL(dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer store.Close()
		feedbackStore = store
		if *queueDir == "" {
			root, err := os.UserConfigDir()
			if err != nil {
				log.Fatal("无法获取本地队列目录")
			}
			*queueDir = filepath.Join(root, "house-rent-table", "persistence")
		}
		queue, err := persistence.OpenQueue(filepath.Join(*queueDir, "tasks.db"))
		if err != nil {
			log.Fatal(err)
		}
		defer queue.Close()
		check, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err = store.CheckMigration(check)
		cancel()
		if err != nil {
			log.Print(err)
		}
		background = &persistence.Worker{Queue: queue, Extractor: agent, Store: store}
		workerContext, stopWorker := context.WithCancel(context.Background())
		workerDone := make(chan struct{})
		go func() { defer close(workerDone); background.Run(workerContext) }()
		defer func() { stopWorker(); <-workerDone }()
		log.Print("已启用后台业务提取与历史保存；刷新或取消报告不会删除后台任务")
	}
	if *template == "" {
		matches, _ := filepath.Glob("outputs/*/租房信息模板.xlsx")
		if len(matches) == 1 {
			*template = matches[0]
		}
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(httpapi.Options{Analyzer: agent, Auth: agent, LocalOnly: true, WebDir: *webDir, TemplatePath: *template, Persistence: background, BrokerFeedback: feedbackStore, DefaultCurrency: defaultCurrency}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
			_ = server.Close()
		}
	}()
	log.Printf("租房检查服务已启动：http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("HTTP 服务启动失败：%v", err)
	}
	stop()
	<-shutdownDone
}
