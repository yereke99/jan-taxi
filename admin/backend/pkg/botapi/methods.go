package botapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// AcquireLease acquires or renews this instance's polling lease.
func (c *Client) AcquireLease(ctx context.Context, instanceID string, ttl time.Duration) (Lease, error) {
	var out Lease
	in := map[string]any{"instance_id": instanceID, "ttl_s": int(ttl / time.Second)}
	err := c.do(ctx, http.MethodPost, "/bots/"+c.Bot+"/lease", nil, in, &out, true)
	return out, err
}

// SaveOffset stores the last processed update id (monotonic, lease holder
// only) and returns the stored value.
func (c *Client) SaveOffset(ctx context.Context, instanceID string, lastUpdateID int64) (int64, error) {
	var out struct {
		LastUpdateID int64 `json:"last_update_id"`
	}
	in := map[string]any{"instance_id": instanceID, "last_update_id": lastUpdateID}
	err := c.do(ctx, http.MethodPost, "/bots/"+c.Bot+"/offset", nil, in, &out, true)
	return out.LastUpdateID, err
}

// ClaimUpdate claims an update before any side effect; false means another
// run already processed it. A retry after a lost response can report false
// for an update this process claimed, so an update is processed at most once.
func (c *Client) ClaimUpdate(ctx context.Context, updateID int64) (bool, error) {
	var out struct {
		Claimed bool `json:"claimed"`
	}
	path := "/bots/" + c.Bot + "/updates/" + strconv.FormatInt(updateID, 10) + "/claim"
	err := c.do(ctx, http.MethodPost, path, nil, nil, &out, true)
	return out.Claimed, err
}

// SyncUser upserts the Telegram user and returns their stored state.
func (c *Client) SyncUser(ctx context.Context, in SyncUserRequest) (SyncUserResponse, error) {
	var out SyncUserResponse
	err := c.do(ctx, http.MethodPost, "/bots/"+c.Bot+"/users/sync", nil, in, &out, true)
	return out, err
}

// SetLanguage stores the user's language.
func (c *Client) SetLanguage(ctx context.Context, tgID int64, lang string) (string, error) {
	var out struct {
		Language string `json:"language"`
	}
	err := c.do(ctx, http.MethodPut, tgPath("/bots/"+c.Bot+"/users", tgID, "/language"), nil,
		map[string]string{"language": lang}, &out, true)
	return out.Language, err
}

func limitQuery(limit int) url.Values {
	if limit <= 0 {
		return nil
	}
	return url.Values{"limit": {strconv.Itoa(limit)}}
}

// PassengerOrders lists a passenger's recent orders, newest first.
func (c *Client) PassengerOrders(ctx context.Context, tgID int64, limit int) ([]OrderSummary, error) {
	var out struct {
		Items []OrderSummary `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, tgPath("/passenger", tgID, "/orders"), limitQuery(limit), nil, &out, true)
	return out.Items, err
}

// DriverSummary returns the driver's status, wallet, active order and corrections.
func (c *Client) DriverSummary(ctx context.Context, tgID int64) (DriverSummary, error) {
	var out DriverSummary
	err := c.do(ctx, http.MethodGet, tgPath("/driver", tgID, "/summary"), nil, nil, &out, true)
	return out, err
}

// SetAvailability takes the driver online or offline.
func (c *Client) SetAvailability(ctx context.Context, tgID int64, online bool) (Availability, error) {
	var out Availability
	err := c.do(ctx, http.MethodPost, tgPath("/driver", tgID, "/availability"), nil,
		map[string]bool{"online": online}, &out, true)
	return out, err
}

// DriverOrders lists the orders the driver can accept now.
func (c *Client) DriverOrders(ctx context.Context, tgID int64, limit int) (AvailableOrders, error) {
	var out AvailableOrders
	err := c.do(ctx, http.MethodGet, tgPath("/driver", tgID, "/orders"), limitQuery(limit), nil, &out, true)
	return out, err
}

// VerifyContact stores the phone of a contact the user shared with the driver bot.
func (c *Client) VerifyContact(ctx context.Context, tgID int64, phone string, contactUserID int64) (ContactResult, error) {
	var out ContactResult
	in := map[string]any{"phone": phone, "contact_user_id": contactUserID}
	err := c.do(ctx, http.MethodPost, tgPath("/driver", tgID, "/contact"), nil, in, &out, true)
	return out, err
}

// ReportLocation stores a location shared in the driver bot. It is not
// retried: a newer location will follow.
func (c *Client) ReportLocation(ctx context.Context, tgID int64, in LocationReport) error {
	return c.do(ctx, http.MethodPost, tgPath("/driver", tgID, "/location"), nil, in, nil, false)
}

// Config returns the bots' runtime configuration.
func (c *Client) Config(ctx context.Context) (Config, error) {
	var out Config
	err := c.do(ctx, http.MethodGet, "/config", nil, nil, &out, true)
	return out, err
}
