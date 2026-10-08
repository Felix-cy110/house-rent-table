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
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/codex"
	"github.com/Felix-cy110/house-rent-table/internal/httpapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "built frontend directory")
	template := flag.String("template", "", "optional XLSX template path")
	codexBinary := flag.String("codex-bin", "", "optional native Codex executable path")
	codexHome := flag.String("codex-home", "", "app-specific Codex credential/config directory")
	codexModel := flag.String("codex-model", "", "optional Codex model override")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
		log.Fatal("本轮仅支持本机单用户运行，请使用 127.0.0.1 或 localhost 监听地址")
	}
	agent := codex.New(codex.Options{Binary: *codexBinary, Home: *codexHome, Model: *codexModel})
	defer agent.Close()
	if *template == "" {
		matches, _ := filepath.Glob("outputs/*/租房信息模板.xlsx")
		if len(matches) == 1 {
			*template = matches[0]
		}
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(httpapi.Options{Analyzer: agent, Auth: agent, LocalOnly: true, WebDir: *webDir, TemplatePath: *template}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 4 * time.Minute, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("租房检查服务已启动：http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
