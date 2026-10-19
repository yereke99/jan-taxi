package notifications

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Photo messages (ARCHITECTURE §22.6): a template with a Photo param is sent
// with sendPhoto and its text as the caption. Private files are uploaded to a
// bot once; the file_id Telegram returns is cached per bot on the files row
// (telegram_file_id_passenger / _driver) and reused. Without a photo, a
// PhotoStore, a PhotoSender, or with a caption over Telegram's limit, the
// message goes out as text.

// maxCaptionRunes is Telegram's photo caption limit.
const maxCaptionRunes = 1024

// maxPhotoBytes bounds an uploaded photo (Telegram accepts up to 10 MB).
const maxPhotoBytes = 10 << 20

// PhotoStore reads stored images and caches their Telegram file ids.
type PhotoStore interface {
	// TelegramFileID returns the cached file id of fileID for bot, or "".
	TelegramFileID(ctx context.Context, fileID uuid.UUID, bot Bot) (string, error)
	// ReadPhoto returns the JPEG bytes of fileID.
	ReadPhoto(ctx context.Context, fileID uuid.UUID) ([]byte, error)
	// RememberTelegramFileID caches the file id Telegram gave for fileID.
	RememberTelegramFileID(ctx context.Context, fileID uuid.UUID, bot Bot, telegramFileID string) error
}

// PhotoSender sends a photo with an HTML caption and returns the message id
// and the file id to reuse.
type PhotoSender interface {
	SendPhoto(ctx context.Context, bot Bot, chatID int64, photo telegram.InputPhoto, caption string,
		markup *telegram.InlineKeyboardMarkup) (messageID int64, telegramFileID string, err error)
}

// FilePhotos is the PhotoStore of the private file storage.
type FilePhotos struct {
	pool  *pgxpool.Pool
	store *storage.Store
}

// NewFilePhotos builds a PhotoStore over files and their bytes.
func NewFilePhotos(pool *pgxpool.Pool, store *storage.Store) *FilePhotos {
	return &FilePhotos{pool: pool, store: store}
}

func fileIDColumn(bot Bot) string {
	if bot == DriverBot {
		return "telegram_file_id_driver"
	}
	return "telegram_file_id_passenger"
}

// TelegramFileID implements PhotoStore.
func (p *FilePhotos) TelegramFileID(ctx context.Context, fileID uuid.UUID, bot Bot) (string, error) {
	var id *string
	err := p.pool.QueryRow(ctx, `SELECT `+fileIDColumn(bot)+` FROM files WHERE id = $1 AND deleted_at IS NULL`, fileID).Scan(&id)
	if db.IsNoRows(err) || id == nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("notifications: photo file id: %w", err)
	}
	return *id, nil
}

// ReadPhoto implements PhotoStore; only JPEG images are sent.
func (p *FilePhotos) ReadPhoto(ctx context.Context, fileID uuid.UUID) ([]byte, error) {
	f, rc, err := p.store.Open(ctx, p.pool, fileID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if f.Mime != "image/jpeg" {
		return nil, errors.New("notifications: photo is not an image")
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxPhotoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("notifications: read photo: %w", err)
	}
	if len(data) > maxPhotoBytes {
		return nil, errors.New("notifications: photo too large")
	}
	return data, nil
}

// RememberTelegramFileID implements PhotoStore.
func (p *FilePhotos) RememberTelegramFileID(ctx context.Context, fileID uuid.UUID, bot Bot, telegramFileID string) error {
	if telegramFileID == "" || len(telegramFileID) > 255 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `UPDATE files SET `+fileIDColumn(bot)+` = $2 WHERE id = $1`, fileID, telegramFileID)
	return err
}

// SendPhoto implements PhotoSender with sendPhoto in HTML parse mode.
func (s *TelegramSender) SendPhoto(ctx context.Context, bot Bot, chatID int64, photo telegram.InputPhoto, caption string,
	markup *telegram.InlineKeyboardMarkup) (int64, string, error) {
	c := s.clients[bot]
	if c == nil {
		return 0, "", ErrBotNotConfigured
	}
	p := telegram.SendPhotoParams{ChatID: chatID, Photo: photo, Caption: caption, ParseMode: "HTML"}
	if markup != nil {
		p.ReplyMarkup = markup
	}
	m, err := c.SendPhoto(ctx, p)
	if err != nil {
		return 0, "", err
	}
	return m.MessageID, m.LargestPhoto(), nil
}
