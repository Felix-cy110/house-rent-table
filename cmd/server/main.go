package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/httpapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "built frontend directory")
	template := flag.String("template", "", "optional XLSX template path")
	flag.Parse()
	if *template == "" {
		matches, _ := filepath.Glob("outputs/*/租房信息模板.xlsx")
		if len(matches) == 1 {
			*template = matches[0]
		}
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(httpapi.Options{WebDir: *webDir, TemplatePath: *template}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second,
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
