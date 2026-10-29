// Package verification provides protected registration identity data and a
// durable, bounded OCR queue. OCR matches supplied text; it proves no authenticity.
package verification

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"golang.org/x/text/unicode/norm"
)

type Options struct {
	Enabled       bool
	Key           string
	AutoApprove   bool
	MinConfidence float64
	QueueLimit    int
}
type Service struct {
	pool *pgxpool.Pool
	opt  Options
	aead cipher.AEAD
	key  []byte
}
type Input struct {
	DocumentType   string `json:"document_type"`
	IIN            string `json:"iin"`
	DocumentNumber string `json:"document_number"`
	Patronymic     string `json:"patronymic"`
}

// Snapshot is encrypted and immutable for one submitted application.
type Snapshot struct {
	Input
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
type Document struct {
	ID     uuid.UUID `json:"id"`
	SHA256 string    `json:"sha256"`
	Kind   string    `json:"kind"`
}

func NewService(pool *pgxpool.Pool, opt Options) (*Service, error) {
	s := &Service{pool: pool, opt: opt}
	if !opt.Enabled {
		return s, nil
	}
	key, err := base64.StdEncoding.DecodeString(opt.Key)
	if err != nil || len(key) != 32 {
		return nil, errors.New("verification: VERIFICATION_KEY must be base64 encoding of 32 random bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	s.aead, err = cipher.NewGCM(block)
	s.key = key
	if opt.MinConfidence <= 0 {
		s.opt.MinConfidence = 98
	}
	if s.opt.MinConfidence < 90 || s.opt.MinConfidence > 100 {
		return nil, errors.New("verification: confidence must be between 90 and 100")
	}
	if opt.QueueLimit <= 0 {
		s.opt.QueueLimit = 1000
	}
	return s, err
}
func (s *Service) Enabled() bool { return s != nil && s.opt.Enabled }
func (s *Service) seal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, b, []byte("jantaxi.driver.identity.v1")), nil
}
func (s *Service) open(b []byte, v any) error {
	if !s.Enabled() || len(b) < s.aead.NonceSize() {
		return errors.New("verification: protected data unavailable")
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, b[:n], b[n:], []byte("jantaxi.driver.identity.v1"))
	if err != nil {
		return errors.New("verification: protected data unreadable")
	}
	if json.Unmarshal(plain, v) != nil {
		return errors.New("verification: protected data invalid")
	}
	return nil
}
func (s *Service) fingerprint(kind, value string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("jantaxi.identity.lookup.v1\x00" + kind + "\x00" + value))
	return m.Sum(nil)
}

var digits12 = regexp.MustCompile(`^[0-9]{12}$`)
var documentNumber = regexp.MustCompile(`^[A-Z0-9]{6,20}$`)

func validDocumentNumber(kind, v string) bool {
	if !documentNumber.MatchString(v) {
		return false
	}
	if kind == "id_card" {
		return len(v) == 9 && strings.Trim(v, "0123456789") == ""
	}
	return kind == "driving_licence" && strings.ContainsAny(v, "0123456789")
}
func NormalizeDocumentNumber(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(v)), " ", ""), "№", "")
}
func ValidIIN(v string) bool {
	if !digits12.MatchString(v) {
		return false
	}
	sum := 0
	for i := 0; i < 11; i++ {
		sum += int(v[i]-'0') * (i + 1)
	}
	c := sum % 11
	if c == 10 {
		sum = 0
		for i := 0; i < 11; i++ {
			sum += int(v[i]-'0') * ((i+2)%11 + 1)
		}
		c = sum % 11
	}
	return c != 10 && c == int(v[11]-'0')
}

// Names retain Kazakh letters; no lossy folding or approximate name matching.
func NormalizeName(v string) string {
	return strings.Join(strings.Fields(strings.ToUpper(norm.NFC.String(v))), " ")
}
func (v Input) Normalize() (Input, error) {
	if v.DocumentType == "" {
		v.DocumentType = "id_card"
	}
	v.IIN = strings.TrimSpace(v.IIN)
	v.DocumentNumber = NormalizeDocumentNumber(v.DocumentNumber)
	v.Patronymic = strings.Join(strings.Fields(norm.NFC.String(v.Patronymic)), " ")
	fields := map[string]string{}
	if v.IIN != "" && !ValidIIN(v.IIN) {
		fields["identity.iin"] = "invalid"
	}
	if v.DocumentType != "id_card" && v.DocumentType != "driving_licence" {
		fields["identity.document_type"] = "invalid"
	}
	if v.DocumentNumber != "" && !validDocumentNumber(v.DocumentType, v.DocumentNumber) {
		fields["identity.document_number"] = "invalid"
	}
	if len([]rune(v.Patronymic)) > 60 {
		fields["identity.patronymic"] = "too_long"
	}
	for _, r := range v.Patronymic {
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !strings.ContainsRune(" '-.", r) {
			fields["identity.patronymic"] = "invalid"
		}
	}
	if len(fields) > 0 {
		return v, httpx.Validation(fields)
	}
	return v, nil
}
func (v Input) Complete() bool {
	return ValidIIN(v.IIN) && validDocumentNumber(v.DocumentType, v.DocumentNumber)
}
func (s *Service) LoadInput(ctx context.Context, q db.Querier, id uuid.UUID) (*Input, int64, error) {
	if !s.Enabled() {
		return nil, 0, nil
	}
	var b []byte
	var version int64
	err := q.QueryRow(ctx, `SELECT ciphertext,version FROM driver_identity_inputs WHERE driver_id=$1`, id).Scan(&b, &version)
	if db.IsNoRows(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("verification: load input: %w", err)
	}
	var v Input
	if err = s.open(b, &v); err != nil {
		return nil, 0, err
	}
	return &v, version, nil
}
func (s *Service) SaveInput(ctx context.Context, tx pgx.Tx, id uuid.UUID, v Input) error {
	if !s.Enabled() {
		return httpx.NewError(http.StatusServiceUnavailable, "verification_unavailable", "identity verification is unavailable")
	}
	v, err := v.Normalize()
	if err != nil {
		return err
	}
	cur, _, err := s.LoadInput(ctx, tx, id)
	if err != nil {
		return err
	}
	if cur != nil && *cur == v {
		return nil
	}
	b, err := s.seal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO driver_identity_inputs(driver_id,ciphertext) VALUES($1,$2) ON CONFLICT(driver_id) DO UPDATE SET ciphertext=$2,version=driver_identity_inputs.version+1,updated_at=now()`, id, b)
	return err
}

var ErrConflict = httpx.Conflict("identity_conflict", "identity or document is already associated with another driver")

// Claim is called only in an approval transaction, never on uncertain OCR/upload.
func (s *Service) Claim(ctx context.Context, tx pgx.Tx, driverID, appID uuid.UUID, in Input) error {
	if !s.Enabled() || !in.Complete() {
		return nil
	}
	for _, key := range []struct{ kind, value string }{{"iin", in.IIN}, {"document", "KAZ\x00" + in.DocumentType + "\x00" + in.DocumentNumber}} {
		fp := s.fingerprint(key.kind, key.value)
		var owner uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO driver_identity_claims(kind,fingerprint,driver_id,application_id) VALUES($1,$2,$3,$4) ON CONFLICT(kind,fingerprint) DO UPDATE SET driver_id=driver_identity_claims.driver_id RETURNING driver_id`, key.kind, fp, driverID, appID).Scan(&owner)
		if err != nil {
			return fmt.Errorf("verification: identity claim failed: %w", err)
		}
		if owner != driverID {
			return ErrConflict
		}
	}
	return nil
}

type Conflict struct {
	Kind     string    `json:"kind"`
	DriverID uuid.UUID `json:"driver_id"`
	Code     string    `json:"driver_code"`
	Status   string    `json:"status"`
}

func (s *Service) Conflicts(ctx context.Context, q db.Querier, driverID uuid.UUID, in Input) ([]Conflict, error) {
	out := []Conflict{}
	if !s.Enabled() || !in.Complete() {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT c.kind,d.id,d.code,d.status FROM driver_identity_claims c JOIN driver_profiles d ON d.id=c.driver_id WHERE c.driver_id<>$1 AND ((c.kind='iin' AND c.fingerprint=$2) OR (c.kind='document' AND c.fingerprint=$3))`, driverID, s.fingerprint("iin", in.IIN), s.fingerprint("document", "KAZ\x00"+in.DocumentType+"\x00"+in.DocumentNumber))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Conflict
		if err := rows.Scan(&v.Kind, &v.DriverID, &v.Code, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
