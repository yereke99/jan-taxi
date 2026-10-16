package driverbot

import (
	"context"
	"fmt"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

func locationReport(l *telegram.Location) botapi.LocationReport {
	r := botapi.LocationReport{Lat: l.Latitude, Lon: l.Longitude, Live: l.LivePeriod > 0}
	if l.HorizontalAccuracy > 0 {
		acc := l.HorizontalAccuracy
		r.AccuracyM = &acc
	}
	if l.Heading > 0 {
		h := float64(l.Heading)
		r.Heading = &h
	}
	return r
}

// onLocation handles a location message: a one-off pin or the start of a
// live location. Only approved drivers report locations.
func (b *Bot) onLocation(ctx context.Context, s *session, loc *telegram.Location) error {
	report := locationReport(loc)
	if s.status() != statusApproved {
		if report.Live {
			b.liveErrors.Allow(s.chatID)
		}
		return b.send(ctx, s, s.t("location_not_approved"), b.keyboard(s))
	}
	err := b.api.ReportLocation(ctx, s.tgID, report)
	switch {
	case err == nil && report.Live:
		b.liveErrors.Reset(s.chatID)
		return b.send(ctx, s, s.t("location_live_on"), b.keyboard(s))
	case err == nil:
		return b.send(ctx, s, s.t("location_saved"), b.keyboard(s))
	case report.Live:
		b.liveErrors.Allow(s.chatID) // the following live updates stay quiet for a while
	}
	return b.locationError(ctx, s, err)
}

// onLiveUpdate stores an update of a live location (an edited message).
// Updates arrive every few seconds, so success is silent and a problem is
// explained at most once per liveErrorEvery per chat.
func (b *Bot) onLiveUpdate(ctx context.Context, msg *telegram.Message) error {
	err := b.api.ReportLocation(ctx, msg.From.ID, locationReport(msg.Location))
	if err == nil {
		return nil
	}
	if !explainable(err) {
		if botapi.IsTransient(err) {
			return fmt.Errorf("live location: %w", err)
		}
		return nil // rate-limited, unknown user or blocked: nothing to tell
	}
	if !b.liveErrors.Allow(msg.Chat.ID) {
		return nil
	}
	s, serr := b.syncSession(ctx, msg, false)
	if serr != nil {
		return serr
	}
	return b.locationError(ctx, s, err)
}

// explainable reports location errors the driver can act on.
func explainable(err error) bool {
	return botapi.IsCode(err, "outside_service_area", "driver_not_approved", "driver_not_registered")
}

func (b *Bot) locationError(ctx context.Context, s *session, err error) error {
	switch {
	case botapi.IsCode(err, "outside_service_area"):
		return b.send(ctx, s, s.t("location_outside"), b.keyboard(s))
	case botapi.IsCode(err, "driver_not_approved", "driver_not_registered"):
		return b.send(ctx, s, s.t("location_not_approved"), b.keyboard(s))
	case botapi.IsCode(err, "too_many_requests"):
		return nil
	}
	_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
	return fmt.Errorf("report location: %w", err)
}
