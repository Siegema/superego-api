package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/siegema/superego-api/internal/accounts"
	"github.com/siegema/superego-api/internal/database"
	"github.com/siegema/superego-api/internal/server"
	"github.com/siegema/superego-api/internal/web"
	"github.com/siegema/superego-api/internal/work"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := database.Open(startup, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	if err := accounts.NewRepository(pool).EnsureDevelopmentUser(startup); err != nil {
		return fmt.Errorf("initialize development user (apply migrations first): %w", err)
	}
	repo := work.NewRepository(pool)
	website, err := web.NewHandler(repo, accounts.DevelopmentUserID)
	if err != nil {
		return fmt.Errorf("initialize website: %w", err)
	}
	handler := server.NewHandler(work.NewHandler(repo, accounts.DevelopmentUserID), website)
	srv := &http.Server{
		// There is no authentication yet. Do not expose this listener publicly.
		Addr:              httpAddress(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	result := make(chan error, 1)
	go func() { result <- srv.ListenAndServe() }()
	slog.Info("development API starting; authentication disabled", "address", srv.Addr)
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
			return err
		}
		return nil
	}
}

func httpAddress() string {
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:8080"
}
