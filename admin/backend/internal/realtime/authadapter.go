package realtime

import (
	"context"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
)

// ServiceAuthenticator adapts *auth.Service to Authenticator.
type ServiceAuthenticator struct {
	Auth *auth.Service
}

var _ Authenticator = ServiceAuthenticator{}

// FromRequest authenticates an admin by session cookie (scope "admin").
func (a ServiceAuthenticator) FromRequest(r *http.Request) (Identity, bool) {
	p, err := a.Auth.AdminFromRequest(r)
	if err != nil {
		return Identity{}, false
	}
	sessionID := p.SessionID
	return Identity{
		Kind:   "admin",
		Scopes: []string{AdminScope},
		Revalidate: func(ctx context.Context) ([]string, error) {
			if err := a.Auth.AdminSessionActive(ctx, sessionID); err != nil {
				return nil, err
			}
			return []string{AdminScope}, nil
		},
	}, true
}

// FromToken authenticates a Mini App token: user:<uid>, plus driver:<id> for
// the driver app once the user has a driver profile.
func (a ServiceAuthenticator) FromToken(ctx context.Context, token string) (Identity, error) {
	p, exp, err := a.Auth.AuthenticateAppToken(ctx, token)
	if err != nil {
		return Identity{}, err
	}
	scopes := func(ctx context.Context) ([]string, error) {
		out := []string{UserScope(p.UserID)}
		if p.App != auth.AppDriver {
			return out, nil
		}
		d, ok, err := a.Auth.DriverProfile(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, DriverScope(d.ID))
		}
		return out, nil
	}
	current, err := scopes(ctx)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Kind:      string(p.App),
		Scopes:    current,
		ExpiresAt: exp,
		Revalidate: func(ctx context.Context) ([]string, error) {
			if err := a.Auth.CheckUserActive(ctx, p.UserID); err != nil {
				return nil, err
			}
			return scopes(ctx)
		},
	}, nil
}
