package botapi

import (
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// acceptResponse is the OpenAPI BotAcceptResult: the order as the driver now
// sees it (with the passenger's contact), whether the driver already held it,
// the commission charged and the wallet after the charge.
type acceptResponse struct {
	Order    orders.DriverOrderDetail `json:"order"`
	Replayed bool                     `json:"replayed"`
	Charged  int64                    `json:"charged"`
	Wallet   *walletView              `json:"wallet"`
}

// driverAccept handles POST /internal/v1/driver/{tg_id}/orders/{id}/accept:
// the driver tapped «Тапсырысты алу» on an offer in the driver bot (§22.4).
// It runs the same atomic acceptance as the Mini App (dispatch.AcceptOrder:
// first tap wins, commission charged in the same transaction) and returns the
// same errors:
// 409 order_already_taken, 402 insufficient_balance, 409 driver_offline,
// 422 location_required, 409 driver_has_active_order, 403
// driver_not_approved, 404 when the order is not in the driver's reach, 409
// invalid_transition when it is no longer searching. The bot edits the tapped
// message itself at once; the offer edit and the trip card with the
// passenger's contact come from the outbox, as for a Mini App acceptance.
func (h *Handler) driverAccept(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	orderID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, err := h.resolveDriver(ctx, tgID)
	if err != nil {
		return err
	}
	res, err := dispatch.AcceptOrder(ctx, h.pool, orderID, p.ID, dispatch.AcceptOptions{ViaBot: true})
	if err != nil {
		return err
	}
	if !res.Replayed {
		h.opts.ArrivalETA.Go(orderID) // the road estimate, after the commit (§22.6)
	}
	out := acceptResponse{Replayed: res.Replayed, Charged: res.Charged}
	if out.Order, err = orders.DriverView(ctx, h.pool, orderID, p.ID); err != nil {
		return err
	}
	if out.Wallet, err = loadWallet(ctx, h.pool, p.ID); err != nil {
		return err
	}
	return httpx.OK(w, out)
}
