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

// profileStep is one Bot API call of a profile.
type profileStep struct {
	name string
	run  func(ctx context.Context) error
}

func profileSteps(api ProfileAPI, p Profile) []profileStep {
	var steps []profileStep
	for _, lang := range sortedKeys(p.Commands) {
		cmds := p.Commands[lang]
		steps = append(steps, profileStep{fmt.Sprintf("setMyCommands %q", lang), func(ctx context.Context) error {
			return api.SetMyCommands(ctx, cmds, lang)
		}})
	}
	for _, lang := range sortedKeys(p.Descriptions) {
		d := p.Descriptions[lang]
		steps = append(steps, profileStep{fmt.Sprintf("setMyDescription %q", lang), func(ctx context.Context) error {
			return api.SetMyDescription(ctx, d, lang)
		}})
	}
	for _, lang := range sortedKeys(p.ShortDescriptions) {
		d := p.ShortDescriptions[lang]
		steps = append(steps, profileStep{fmt.Sprintf("setMyShortDescription %q", lang), func(ctx context.Context) error {
			return api.SetMyShortDescription(ctx, d, lang)
		}})
	}
	if p.MenuButton != nil && IsHTTPS(p.MenuButton.WebApp.URL) {
		button := *p.MenuButton
		steps = append(steps, profileStep{"setChatMenuButton", func(ctx context.Context) error {
			return api.SetChatMenuButton(ctx, nil, button)
		}})
	}
	return steps
}

// ApplyProfile publishes p. All calls are attempted; errors are joined.
func ApplyProfile(ctx context.Context, api ProfileAPI, p Profile) error {
	var errs []error
	for _, st := range profileSteps(api, p) {
		if err := st.run(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.name, err))
		}
	}
	return errors.Join(errs...)
}

// profileBackoff bounds the delays between publish attempts.
var profileBackoff = telegram.Backoff{Min: 2 * time.Second, Max: 5 * time.Minute}

// ApplyProfileWithRetry publishes p and retries the calls that failed with a
// retryable error (network, 429, 5xx) until they succeed or ctx ends, so the
// default menu button always ends up with the current Mini App URL. A call
// Telegram rejects (4xx) is logged and not repeated. The bot works without
// its profile meanwhile.
func ApplyProfileWithRetry(ctx context.Context, api ProfileAPI, p Profile, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if p.MenuButton == nil {
		log.Warn("menu button not set: no Mini App URL is known")
	} else if !IsHTTPS(p.MenuButton.WebApp.URL) {
		log.Warn("menu button not set: the Mini App URL must be https", slog.String("url", p.MenuButton.WebApp.URL))
	}
	b := profileBackoff
	pending := profileSteps(api, p)
	rejected := 0
	for attempt := 1; len(pending) > 0; attempt++ {
		var failed []profileStep
		var wait time.Duration
		for _, st := range pending {
			err := st.run(ctx)
			switch {
			case err == nil:
			case ctx.Err() != nil:
				return
			case telegram.IsRetryable(err):
				failed = append(failed, st)
				if d, ok := telegram.RetryAfter(err); ok {
					wait = max(wait, d)
				}
				log.Warn("publish bot profile failed", slog.String("call", st.name), slog.Int("attempt", attempt),
					slog.String("err", err.Error()))
			default:
				rejected++
				log.Error("bot profile call rejected", slog.String("call", st.name), slog.String("err", err.Error()))
			}
		}
		if pending = failed; len(pending) == 0 {
			break
		}
		if wait == 0 {
			wait = b.Next()
		}
		sleep(ctx, wait)
		if ctx.Err() != nil {
			return
		}
	}
	if rejected > 0 {
		log.Warn("bot profile published with rejected calls", slog.Int("rejected", rejected))
		return
	}
	log.Info("bot profile published")
}
