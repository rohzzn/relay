package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rohzzn/relay/internal/config"
	"github.com/rohzzn/relay/internal/db"
	"github.com/rohzzn/relay/internal/server"
	webfs "github.com/rohzzn/relay/web"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("relay: ")

	// ── Healthcheck subcommand (used by Docker HEALTHCHECK) ───────────────────
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		port := os.Getenv("RELAY_PORT")
		if port == "" {
			port = "8080"
		}
		resp, err := http.Get(fmt.Sprintf("http://localhost:%s/healthz", port))
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// ── Config ──────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// ── Database ─────────────────────────────────────────────────────────────
	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	log.Printf("database opened at %s/relay.db", cfg.DataDir)

	// ── Retention pruner ─────────────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := database.PruneChecks(cfg.RetentionDays); err != nil {
				log.Printf("prune checks: %v", err)
			}
		}
	}()

	// ── HTTP server ──────────────────────────────────────────────────────────
	srv, err := server.New(cfg, database, webfs.FS)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	// ── Graceful shutdown ────────────────────────────────────────────────────
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Start() }()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("server: %v", err)
		}
	case sig := <-stop:
		log.Printf("received %s — shutting down…", sig)
		_, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		database.Close()
		os.Exit(0)
	}
}
