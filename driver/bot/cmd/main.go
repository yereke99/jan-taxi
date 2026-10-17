// Command driver-bot runs the Jan Taxi driver Telegram bot
// (long polling). "driver-bot healthcheck" probes the local health
// endpoint for Docker HEALTHCHECK.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
	"github.com/yereke99/jantaxi/driver/bot/internal/driverbot"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runtime.HealthcheckCommand())
	}
	log := runtime.NewLogger(os.Stderr, "driver-bot")
	if err := run(log); err != nil {
		log.Error("driver bot failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := driverbot.LoadConfig()
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings() {
		log.Warn("configuration warning", slog.String("warning", w))
	}
	tg := telegram.New(cfg.Token, telegram.WithBaseURL(cfg.TelegramAPIBaseURL), telegram.WithLogger(log))
	api := botapi.New(cfg.InternalAPIURL, cfg.ServiceSecret, botapi.BotDriver)
	bot, err := driverbot.New(cfg, tg, api, log)
	if err != nil {
		return err
	}
	ctx, stop := runtime.SignalContext()
	defer stop()
	return runtime.Bot{
		Name:       botapi.BotDriver,
		Telegram:   tg,
		API:        api,
		Handler:    bot,
		InstanceID: cfg.InstanceID,
		HealthAddr: net.JoinHostPort("", cfg.HealthPort),
		Log:        log,
		OnLeader: func(ctx context.Context) {
			runtime.ApplyProfileWithRetry(ctx, tg, bot.Profile(ctx), log)
		},
	}.Run(ctx)
}
