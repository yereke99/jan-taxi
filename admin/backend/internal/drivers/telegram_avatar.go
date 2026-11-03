package drivers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

var (
	errTelegramPhotoUnavailable = httpx.NewError(http.StatusUnprocessableEntity, "telegram_photo_unavailable", "Telegram profile photo is unavailable; choose another photo")
	errTelegramPhotoFailed      = httpx.NewError(http.StatusBadGateway, "telegram_photo_failed", "could not download the Telegram profile photo; try again or choose another photo")
)

// importTelegramAvatar accepts the launch itself, never an arbitrary image URL.
// Telegram's signature and the bearer identity must identify the same driver.
func (h *Handler) importTelegramAvatar(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	var req struct {
		InitData string `json:"init_data"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.InitData == "" {
		return httpx.Validation(map[string]string{"init_data": "required"})
	}
	if h.opt.TelegramAuth == nil {
		return auth.ErrInvalidInitData
	}
	launch, err := h.opt.TelegramAuth.ValidateInitData(req.InitData, auth.AppDriver, time.Now())
	if err != nil {
		return err
	}
	if launch.User.ID != p.TelegramID {
		return httpx.Forbidden()
	}
	photoURL, ok := telegramPhotoURL(launch.User.PhotoURL)
	if !ok {
		return errTelegramPhotoUnavailable
	}
	ctx := r.Context()
	prof, err := GetProfile(ctx, h.pool, d.ID)
	if err != nil {
		return err
	}
	if err := checkAvatarEditable(ctx, h.pool, prof); err != nil {
		return err
	}
	data, err := fetchTelegramPhoto(ctx, h.telegramPhotoClient, photoURL, min(h.opt.MaxUploadBytes, MaxAvatarBytes))
	if err != nil {
		return err
	}
	f, err := h.saveAvatar(ctx, p.UserID, d.ID, data)
	if err != nil {
		return err
	}
	return httpx.Created(w, f)
}

// Telegram userpics are HTTPS resources under this dedicated public path.
// Refuse credentials, alternate ports, redirects and general-purpose t.me links.
func telegramPhotoURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "t.me" || u.User != nil || u.Fragment != "" ||
		!strings.HasPrefix(u.Path, "/i/userpic/") || strings.Contains(u.Path, "..") || strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		return nil, false
	}
	return u, true
}

func fetchTelegramPhoto(ctx context.Context, client *http.Client, u *url.URL, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errTelegramPhotoUnavailable
	}
	req.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	res, err := client.Do(req)
	if err != nil {
		return nil, errTelegramPhotoFailed
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errTelegramPhotoFailed
	}
	if res.ContentLength > limit {
		return nil, storage.ErrFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, errTelegramPhotoFailed
	}
	if int64(len(data)) > limit {
		return nil, storage.ErrFileTooLarge
	}
	return data, nil
}

func newTelegramPhotoClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialTelegramPhoto
	return &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Resolve once, reject non-public addresses and dial the checked IP directly so
// a second DNS lookup cannot turn a public hostname into an internal request.
func dialTelegramPhoto(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "t.me" || port != "443" {
		return nil, errors.New("invalid Telegram photo address")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("could not resolve Telegram photo host")
	}
	for _, ip := range ips {
		if !publicPhotoIP(ip) {
			return nil, errors.New("Telegram photo resolved to a non-public address")
		}
	}
	var dialErr error
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr == nil {
		dialErr = errors.New("Telegram photo host has no addresses")
	}
	return nil, dialErr
}

func publicPhotoIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Carrier NAT and special-use IPv4/IPv6 addresses are not public servers.
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
