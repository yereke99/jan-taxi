package marketing

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// localized decodes a stored {kk, ru, en} text.
func localized(raw []byte) settings.Localized {
	var l settings.Localized
	_ = json.Unmarshal(raw, &l)
	return l
}

// Mini App list cursors are ledger ids.
func formatCursor(id int64) string { return strconv.FormatInt(id, 10) }

func parseCursor(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}

// publishWallet tells a driver's apps that the wallet changed.
func publishWallet(ctx context.Context, q db.Querier, driverID uuid.UUID) error {
	return realtime.Publish(ctx, q, realtime.Event{Type: realtime.WalletUpdated,
		Scopes: []string{realtime.DriverScope(driverID)},
		Data:   map[string]any{"driver_id": driverID.String(), "reason": "bonus_credit"}})
}
