package main

import (
	"log"
	"os"

	"mooliq/backend/internal/api"
	"mooliq/backend/internal/db"
)

func main() {
	dbPath := os.Getenv("MOOLIQ_DB_PATH")
	if dbPath == "" {
		dbPath = "mooliq.db"
	}
	addr := os.Getenv("MOOLIQ_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	conn, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer conn.Close()

	server := api.NewServer(conn)
	if err := server.Start(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
