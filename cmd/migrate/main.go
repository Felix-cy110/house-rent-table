package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/persistence"
)

func main() {
	dsn := os.Getenv("RENT_MYSQL_DSN")
	if dsn == "" {
		log.Fatal("请通过 RENT_MYSQL_DSN 指定已创建的目标数据库")
	}
	store, err := persistence.OpenMySQL(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = store.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("租房业务数据库迁移完成（版本 1）")
}
