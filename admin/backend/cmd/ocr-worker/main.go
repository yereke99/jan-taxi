package main

import (
	"context"
	"fmt"
	"github.com/yereke99/jantaxi/admin/backend/internal/moderation"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/verification"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("OCR worker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL, 4)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc, err := verification.NewService(pool, verification.Options{Enabled: cfg.VerificationEnabled, Key: cfg.VerificationKey, AutoApprove: cfg.VerificationAutoApprove, MinConfidence: cfg.VerificationMinConfidence, QueueLimit: cfg.VerificationQueueLimit})
	if err != nil {
		return err
	}
	// The API owns storage creation/migrations. The worker's mount is read-only.
	store := &storage.Store{Dir: cfg.FileStorageDir}
	mh := moderation.NewHandler(pool).WithVerification(svc)
	timeout := 60 * time.Second
	if raw := os.Getenv("OCR_TIMEOUT"); raw != "" {
		timeout, err = time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid OCR_TIMEOUT")
		}
	}
	poll := 2 * time.Second
	if raw := os.Getenv("OCR_POLL_INTERVAL"); raw != "" {
		poll, err = time.ParseDuration(raw)
		if err != nil || poll < time.Second {
			return fmt.Errorf("invalid OCR_POLL_INTERVAL")
		}
	}
	attempts := 3
	if raw := os.Getenv("OCR_MAX_ATTEMPTS"); raw != "" {
		attempts, err = strconv.Atoi(raw)
		if err != nil || attempts < 1 || attempts > 10 {
			return fmt.Errorf("invalid OCR_MAX_ATTEMPTS")
		}
	}
	return svc.Run(ctx, verification.LocalEngine{Service: svc, Store: store}, mh.AutoApprove, verification.WorkerOptions{Timeout: timeout, PollInterval: poll, MaxAttempts: attempts})
}
