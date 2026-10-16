package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// ProfileAPI is the part of the Bot API used to publish the bot profile.
type ProfileAPI interface {
	SetMyCommands(ctx context.Context, commands []telegram.BotCommand, languageCode string) error
	SetMyDescription(ctx context.Context, description, languageCode string) error
	SetMyShortDescription(ctx context.Context, short, languageCode string) error
	SetChatMenuButton(ctx context.Context, chatID *int64, button telegram.MenuButtonWebApp) error
}

// Profile is the bot's command menu, descriptions and default menu button.
// Map keys are Telegram language codes; "" is the fallback for every other
// language (Kazakh for Jan Taxi).
type Profile struct {
	Commands          map[string][]telegram.BotCommand
	Descriptions      map[string]string
	ShortDescriptions map[string]string
	MenuButton        *telegram.MenuButtonWebApp
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ApplyProfile publishes p. All calls are attempted; errors are joined.
func ApplyProfile(ctx context.Context, api ProfileAPI, p Profile) error {
	var errs []error
	for _, lang := range sortedKeys(p.Commands) {
		if err := api.SetMyCommands(ctx, p.Commands[lang], lang); err != nil {
			errs = append(errs, fmt.Errorf("setMyCommands %q: %w", lang, err))
		}
	}
	for _, lang := range sortedKeys(p.Descriptions) {
		if err := api.SetMyDescription(ctx, p.Descriptions[lang], lang); err != nil {
			errs = append(errs, fmt.Errorf("setMyDescription %q: %w", lang, err))
		}
	}
	for _, lang := range sortedKeys(p.ShortDescriptions) {
		if err := api.SetMyShortDescription(ctx, p.ShortDescriptions[lang], lang); err != nil {
			errs = append(errs, fmt.Errorf("setMyShortDescription %q: %w", lang, err))
		}
	}
	if p.MenuButton != nil && IsHTTPS(p.MenuButton.WebApp.URL) {
		if err := api.SetChatMenuButton(ctx, nil, *p.MenuButton); err != nil {
			errs = append(errs, fmt.Errorf("setChatMenuButton: %w", err))
		}
	}
	return errors.Join(errs...)
}

// ApplyProfileWithRetry publishes p, retrying failed attempts with backoff
// (up to 5 attempts). Failures are logged; the bot works without them.
func ApplyProfileWithRetry(ctx context.Context, api ProfileAPI, p Profile, log *slog.Logger) {
	if p.MenuButton != nil && !IsHTTPS(p.MenuButton.WebApp.URL) && log != nil {
		log.Warn("menu button not set: the Mini App URL must be https", slog.String("url", p.MenuButton.WebApp.URL))
	}
	b := telegram.Backoff{Min: 2 * time.Second, Max: time.Minute}
	for attempt := 1; attempt <= 5; attempt++ {
		err := ApplyProfile(ctx, api, p)
		if err == nil {
			if log != nil {
				log.Info("bot profile published")
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		if log != nil {
			log.Warn("publish bot profile failed", slog.Int("attempt", attempt), slog.String("err", err.Error()))
		}
		wait := b.Next()
		if d, ok := telegram.RetryAfter(err); ok {
			wait = d
		}
		sleep(ctx, wait)
	}
}
