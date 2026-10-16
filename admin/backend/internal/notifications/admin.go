package notifications

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Admin serves the admin notification endpoints (ARCHITECTURE §19).
type Admin struct {
	pool *pgxpool.Pool
}

// NewAdmin creates the admin handlers.
func NewAdmin(pool *pgxpool.Pool) *Admin { return &Admin{pool: pool} }

// MountAdmin registers the routes on the admin router (which already carries
// RequireAdmin); each route adds its permission.
func (h *Admin) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermOrdersManage)).Get("/notifications", httpx.H(h.list))
	r.With(auth.Permit(auth.PermOrdersManage)).Post("/notifications/{id}/retry", httpx.H(h.retry))
	r.With(auth.Permit(auth.PermConfigManage)).Get("/notification-templates", httpx.H(h.listTemplates))
	r.With(auth.Permit(auth.PermConfigManage)).Put("/notification-templates/{key}/{lang}", httpx.H(h.putTemplate))
	r.With(auth.Permit(auth.PermConfigManage)).Delete("/notification-templates/{key}/{lang}", httpx.H(h.deleteTemplate))
}

// OutboxItem is an outbox row as shown to admins.
type OutboxItem struct {
	ID                uuid.UUID       `json:"id"`
	Bot               string          `json:"bot"`
	UserID            *uuid.UUID      `json:"user_id"`
	UserName          string          `json:"user_name"`
	Username          *string         `json:"username"`
	ChatID            int64           `json:"chat_id"`
	Template          string          `json:"template"`
	Params            json.RawMessage `json:"params"`
	DedupeKey         string          `json:"dedupe_key"`
	Status            string          `json:"status"`
	Attempts          int             `json:"attempts"`
	MaxAttempts       int             `json:"max_attempts"`
	NextAttemptAt     time.Time       `json:"next_attempt_at"`
	LockedUntil       *time.Time      `json:"locked_until"`
	LastError         *string         `json:"last_error"`
	TelegramMessageID *int64          `json:"telegram_message_id"`
	CreatedAt         time.Time       `json:"created_at"`
	SentAt            *time.Time      `json:"sent_at"`
	AttemptsSummary   AttemptsSummary `json:"attempts_summary"`
}

// AttemptsSummary aggregates notification_attempts for one row.
type AttemptsSummary struct {
	Total int          `json:"total"`
	Sent  int          `json:"sent"`
	Retry int          `json:"retry"`
	Fatal int          `json:"fatal"`
	Last  *AttemptInfo `json:"last"`
}

// AttemptInfo is the most recent attempt.
type AttemptInfo struct {
	Attempt     int       `json:"attempt"`
	Outcome     string    `json:"outcome"`
	HTTPStatus  *int      `json:"http_status"`
	Error       *string   `json:"error"`
	RetryAfterS *int      `json:"retry_after_s"`
	FinishedAt  time.Time `json:"finished_at"`
}

var validStatuses = map[string]bool{StatusPending: true, StatusSending: true, StatusSent: true, StatusDead: true, StatusCancelled: true}

const outboxSelect = `
	SELECT o.id, o.bot, o.user_id, trim(concat_ws(' ', u.first_name, u.last_name)), u.username, o.chat_id, o.template,
	       o.params, o.dedupe_key, o.status, o.attempts, o.max_attempts, o.next_attempt_at, o.locked_until,
	       o.last_error, o.telegram_message_id, o.created_at, o.sent_at,
	       COALESCE(s.total, 0), COALESCE(s.sent, 0), COALESCE(s.retry, 0), COALESCE(s.fatal, 0),
	       la.attempt, la.outcome, la.http_status, la.error, la.retry_after_s, la.finished_at
	FROM notification_outbox o
	LEFT JOIN users u ON u.id = o.user_id
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS total,
		       count(*) FILTER (WHERE outcome = 'sent')::int AS sent,
		       count(*) FILTER (WHERE outcome = 'retry')::int AS retry,
		       count(*) FILTER (WHERE outcome = 'fatal')::int AS fatal
		FROM notification_attempts WHERE outbox_id = o.id
	) s ON true
	LEFT JOIN LATERAL (
		SELECT attempt, outcome, http_status, error, retry_after_s, finished_at
		FROM notification_attempts WHERE outbox_id = o.id ORDER BY id DESC LIMIT 1
	) la ON true`

func scanOutbox(row pgx.Row) (OutboxItem, error) {
	var (
		it         OutboxItem
		params     []byte
		userName   *string
		attempt    *int
		outcome    *string
		httpStatus *int
		errText    *string
		retryAfter *int
		finished   *time.Time
	)
	err := row.Scan(&it.ID, &it.Bot, &it.UserID, &userName, &it.Username, &it.ChatID, &it.Template,
		&params, &it.DedupeKey, &it.Status, &it.Attempts, &it.MaxAttempts, &it.NextAttemptAt, &it.LockedUntil,
		&it.LastError, &it.TelegramMessageID, &it.CreatedAt, &it.SentAt,
		&it.AttemptsSummary.Total, &it.AttemptsSummary.Sent, &it.AttemptsSummary.Retry, &it.AttemptsSummary.Fatal,
		&attempt, &outcome, &httpStatus, &errText, &retryAfter, &finished)
	if err != nil {
		return OutboxItem{}, err
	}
	it.Params = params
	if userName != nil {
		it.UserName = *userName
	}
	if attempt != nil && outcome != nil && finished != nil {
		it.AttemptsSummary.Last = &AttemptInfo{Attempt: *attempt, Outcome: *outcome, HTTPStatus: httpStatus,
			Error: errText, RetryAfterS: retryAfter, FinishedAt: *finished}
	}
	return it, nil
}

// list: GET /admin/notifications?status=&bot=&template=&user_id=&page=&page_size=
func (h *Admin) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status != "" && !validStatuses[status] {
		return httpx.Validation(map[string]string{"status": "invalid"})
	}
	bot := strings.TrimSpace(q.Get("bot"))
	if bot != "" && !Bot(bot).Valid() {
		return httpx.Validation(map[string]string{"bot": "invalid"})
	}
	tpl := strings.TrimSpace(q.Get("template"))
	if tpl != "" {
		if _, ok := Lookup(tpl); !ok {
			return httpx.Validation(map[string]string{"template": "invalid"})
		}
	}
	userID, err := httpx.QueryUUID(r, "user_id")
	if err != nil {
		return err
	}
	const where = ` WHERE ($1 = '' OR o.status = $1) AND ($2 = '' OR o.bot = $2) AND ($3 = '' OR o.template = $3)
		AND ($4::uuid IS NULL OR o.user_id = $4)`
	args := []any{status, bot, tpl, userID}

	var total int
	if err := h.pool.QueryRow(r.Context(), `SELECT count(*) FROM notification_outbox o`+where, args...).Scan(&total); err != nil {
		return httpx.Internal(err)
	}
	rows, err := h.pool.Query(r.Context(), outboxSelect+where+` ORDER BY o.created_at DESC, o.id DESC LIMIT $5 OFFSET $6`,
		append(args, page.PageSize, page.Offset())...)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	items := []OutboxItem{}
	for rows.Next() {
		it, err := scanOutbox(rows)
		if err != nil {
			return httpx.Internal(err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, httpx.Page[OutboxItem]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

// retry: POST /admin/notifications/{id}/retry — dead/cancelled → pending with a
// fresh attempt budget.
func (h *Admin) retry(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var item OutboxItem
	err = db.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		var (
			status    string
			attempts  int
			lastError *string
		)
		err := tx.QueryRow(r.Context(), `SELECT status, attempts, last_error FROM notification_outbox WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &attempts, &lastError)
		if db.IsNoRows(err) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if status != StatusDead && status != StatusCancelled {
			return httpx.Conflict("invalid_transition", "only dead or cancelled notifications can be retried").
				WithDetails(map[string]any{"status": status})
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE notification_outbox
			SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL,
			    locked_until = NULL, locked_by = NULL
			WHERE id = $1`, id); err != nil {
			return err
		}
		if err := audit.Log(r.Context(), tx, r, audit.Entry{
			Action: "notification.retry", EntityType: "notification", EntityID: id.String(),
			Before: map[string]any{"status": status, "attempts": attempts, "last_error": lastError},
			After:  map[string]any{"status": StatusPending, "attempts": 0},
		}); err != nil {
			return err
		}
		item, err = scanOutbox(tx.QueryRow(r.Context(), outboxSelect+` WHERE o.id = $1`, id))
		return err
	})
	if err != nil {
		return httpx.AsError(err)
	}
	return httpx.OK(w, item)
}

// TemplateItem is one template in one language, shaped as NotificationTemplate
// in api/openapi.yaml. Bots, Params, Button and OverrideBody are additive fields
// for the admin editor.
type TemplateItem struct {
	Key          string      `json:"key"`
	Language     string      `json:"language"`
	Body         string      `json:"body"` // effective text
	IsCustom     bool        `json:"is_custom"`
	DefaultBody  string      `json:"default_body"`
	Placeholders []string    `json:"placeholders"`
	UpdatedAt    *time.Time  `json:"updated_at"`
	UpdatedBy    *AdminRef   `json:"updated_by"`
	OverrideBody *string     `json:"override_body"` // stored override, even if it no longer validates
	Bots         []Bot       `json:"bots"`
	Params       []Param     `json:"params"`
	Button       *ButtonView `json:"button"`
}

// AdminRef identifies the admin who last edited an override.
type AdminRef struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
}

// ButtonView is the template's inline button in one language.
type ButtonView struct {
	Label      string `json:"label"`
	StartParam string `json:"start_param"`
}

type overrideRow struct {
	body      string
	updatedAt time.Time
	updatedBy *AdminRef
}

func loadOverrideRows(r *http.Request, q db.Querier, key string) (map[overrideKey]overrideRow, error) {
	rows, err := q.Query(r.Context(), `
		SELECT t.key, t.language, t.body, t.updated_at, a.id, a.email::text, a.full_name
		FROM notification_templates t LEFT JOIN admin_users a ON a.id = t.updated_by
		WHERE $1 = '' OR t.key = $1`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[overrideKey]overrideRow{}
	for rows.Next() {
		var (
			k        overrideKey
			o        overrideRow
			adminID  *uuid.UUID
			email    *string
			fullName *string
		)
		if err := rows.Scan(&k.key, &k.lang, &o.body, &o.updatedAt, &adminID, &email, &fullName); err != nil {
			return nil, err
		}
		if adminID != nil {
			o.updatedBy = &AdminRef{ID: *adminID, Email: deref(email), FullName: deref(fullName)}
		}
		out[k] = o
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func templateItem(t *Template, lang string, ov map[overrideKey]overrideRow) TemplateItem {
	params := t.Params
	if params == nil {
		params = []Param{}
	}
	it := TemplateItem{
		Key: t.Key, Language: lang, Body: t.DefaultBody(lang), DefaultBody: t.DefaultBody(lang),
		Placeholders: paramNames(t), Bots: t.Bots, Params: params,
	}
	if t.Button != nil {
		it.Button = &ButtonView{Label: t.Button.Label[lang], StartParam: t.Button.StartParam}
	}
	if o, ok := ov[overrideKey{t.Key, lang}]; ok {
		body, at := o.body, o.updatedAt
		it.IsCustom, it.OverrideBody, it.UpdatedAt, it.UpdatedBy = true, &body, &at, o.updatedBy
		if t.ValidateBody(body) == nil {
			it.Body = body
		}
	}
	return it
}

// listTemplates: GET /admin/notification-templates — every key × language.
func (h *Admin) listTemplates(w http.ResponseWriter, r *http.Request) error {
	ov, err := loadOverrideRows(r, h.pool, "")
	if err != nil {
		return httpx.Internal(err)
	}
	items := make([]TemplateItem, 0, len(registry)*len(Languages))
	for _, k := range Keys() {
		t, _ := Lookup(k)
		for _, lang := range Languages {
			items = append(items, templateItem(t, lang, ov))
		}
	}
	return httpx.OK(w, map[string]any{"items": items, "max_body_length": MaxBodyLength})
}

func templateParams(r *http.Request) (*Template, string, error) {
	t, ok := Lookup(chi.URLParam(r, "key"))
	if !ok {
		return nil, "", httpx.NotFound()
	}
	lang := chi.URLParam(r, "lang")
	if !validLanguage(lang) {
		return nil, "", httpx.NotFound()
	}
	return t, lang, nil
}

// putTemplate: PUT /admin/notification-templates/{key}/{lang} {"body": "..."}
func (h *Admin) putTemplate(w http.ResponseWriter, r *http.Request) error {
	t, lang, err := templateParams(r)
	if err != nil {
		return err
	}
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	body := strings.TrimSpace(strings.ReplaceAll(req.Body, "\r\n", "\n"))
	if body == "" {
		return httpx.Validation(map[string]string{"body": "required"})
	}
	if utf8.RuneCountInString(body) > MaxBodyLength {
		return httpx.Validation(map[string]string{"body": "too_long"})
	}
	if err := t.ValidateBody(body); err != nil {
		return httpx.Validation(map[string]string{"body": "invalid"}).
			WithDetails(map[string]any{"reason": err.Error(), "allowed_placeholders": paramNames(t)})
	}
	var item TemplateItem
	err = db.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		var before *string
		err := tx.QueryRow(r.Context(), `SELECT body FROM notification_templates WHERE key = $1 AND language = $2 FOR UPDATE`,
			t.Key, lang).Scan(&before)
		if err != nil && !db.IsNoRows(err) {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO notification_templates (key, language, body, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (key, language) DO UPDATE
			SET body = EXCLUDED.body, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			t.Key, lang, body, admin.AdminID); err != nil {
			return err
		}
		if err := audit.Log(r.Context(), tx, r, audit.Entry{
			Action: "notification_template.update", EntityType: "notification_template", EntityID: t.Key + "/" + lang,
			Before: map[string]any{"body": before}, After: map[string]any{"body": body},
		}); err != nil {
			return err
		}
		ov, err := loadOverrideRows(r, tx, t.Key)
		if err != nil {
			return err
		}
		item = templateItem(t, lang, ov)
		return nil
	})
	if err != nil {
		return httpx.AsError(err)
	}
	return httpx.OK(w, item)
}

// deleteTemplate: DELETE /admin/notification-templates/{key}/{lang} — revert to
// the default body. Deleting a missing override is a no-op.
func (h *Admin) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	t, lang, err := templateParams(r)
	if err != nil {
		return err
	}
	var item TemplateItem
	err = db.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		var before string
		err := tx.QueryRow(r.Context(), `DELETE FROM notification_templates WHERE key = $1 AND language = $2 RETURNING body`,
			t.Key, lang).Scan(&before)
		switch {
		case db.IsNoRows(err):
		case err != nil:
			return err
		default:
			if err := audit.Log(r.Context(), tx, r, audit.Entry{
				Action: "notification_template.revert", EntityType: "notification_template", EntityID: t.Key + "/" + lang,
				Before: map[string]any{"body": before}, After: map[string]any{"body": nil},
			}); err != nil {
				return err
			}
		}
		ov, err := loadOverrideRows(r, tx, t.Key)
		if err != nil {
			return err
		}
		item = templateItem(t, lang, ov)
		return nil
	})
	if err != nil {
		return httpx.AsError(err)
	}
	return httpx.OK(w, item)
}

func paramNames(t *Template) []string {
	out := make([]string, 0, len(t.Params))
	for _, p := range t.Params {
		out = append(out, p.Name)
	}
	return out
}
