package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

type Engine interface {
	Read(context.Context, Job) (Result, error)
}
type LocalEngine struct {
	Service    *Service
	Store      *storage.Store
	Recognizer string
}
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("engine output limit")
	}
	return b.Buffer.Write(p)
}
func run(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1", "OPENBLAS_NUM_THREADS=1", "TMPDIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	out := &boundedBuffer{limit: 2 << 20}
	discard := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = discard
	if err := cmd.Run(); err != nil {
		code, exit := "failed", 0
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			code = "timeout"
		case errors.Is(ctx.Err(), context.Canceled):
			code = "interrupted"
		case errors.Is(err, exec.ErrNotFound):
			code = "unavailable"
		default:
			var e *exec.ExitError
			if errors.As(err, &e) {
				code, exit = "exit", e.ExitCode()
			}
		}
		return nil, &processingError{Stage: bin, Code: code, ExitCode: exit}
	}
	return out.Bytes(), nil
}

// Check fails at startup if the installed executables or language models are
// missing, rather than silently retrying every driver's application.
func (e LocalEngine) Check(ctx context.Context) error {
	bins := []string{"pdfinfo", "pdftotext", "pdftoppm"}
	if e.Recognizer != "" {
		bins = append(bins, "python3")
	} else {
		bins = append(bins, "tesseract")
	}
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			return &processingError{Stage: bin, Code: "unavailable"}
		}
	}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e.Recognizer != "" {
		_, err := run(check, os.TempDir(), "python3", e.Recognizer, "--check")
		return err
	}
	b, err := run(check, os.TempDir(), "tesseract", "--list-langs")
	if err != nil {
		return err
	}
	for _, lang := range []string{"kaz", "rus", "eng"} {
		found := false
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == lang {
				found = true
			}
		}
		if !found {
			return &processingError{Stage: "tesseract", Code: "language_missing"}
		}
	}
	return nil
}
func (e LocalEngine) Read(ctx context.Context, j Job) (Result, error) {
	dir, err := os.MkdirTemp("", "jantaxi-ocr-")
	if err != nil {
		return Result{}, errors.New("temporary storage unavailable")
	}
	defer os.RemoveAll(dir)
	var lines []Line
	var embedded []Line
	var sources []string
	cardSources := map[string]bool{}
	seen := map[string]bool{}
	duplicates := 0
	for i, d := range j.Documents {
		if seen[d.SHA256] {
			duplicates++
			continue
		}
		seen[d.SHA256] = true
		meta, rc, err := e.Store.Open(ctx, e.Service.pool, d.ID)
		if err != nil {
			return Result{}, errors.New("document unavailable")
		}
		if meta.SHA256 != d.SHA256 || (meta.Purpose != storage.PurposeIDFront && meta.Purpose != storage.PurposeIDBack) {
			rc.Close()
			return Result{}, errors.New("document version changed")
		}
		p := filepath.Join(dir, strconv.Itoa(i))
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			rc.Close()
			return Result{}, err
		}
		n, copyErr := io.Copy(f, io.LimitReader(rc, (10<<20)+1))
		rc.Close()
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil || n > 10<<20 {
			return Result{}, errors.New("document input limit")
		}
		images := []string{p}
		if meta.Mime == storage.MimePDF {
			info, err := run(ctx, dir, "pdfinfo", p)
			if err != nil {
				return Result{}, err
			}
			pages := 0
			for _, line := range strings.Split(string(info), "\n") {
				if strings.HasPrefix(line, "Pages:") {
					pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
				}
			}
			if pages < 1 || pages > 2 {
				return Result{}, errors.New("document page limit")
			}
			// Read the bounded text layer before raster OCR. Text in a PDF is
			// applicant-controlled (and may be invisible), so it is corroboration,
			// never a replacement for recognition of the visible document.
			if raw, err := run(ctx, dir, "pdftotext", "-layout", "-nopgbrk", "-f", "1", "-l", strconv.Itoa(pages), p, "-"); err == nil {
				for _, line := range strings.Split(string(raw), "\n") {
					if strings.TrimSpace(line) != "" {
						embedded = append(embedded, Line{Text: strings.TrimSpace(line), Confidence: 100})
					}
				}
			}
			prefix := p + "-page"
			if _, err = run(ctx, dir, "pdftoppm", "-f", "1", "-l", strconv.Itoa(pages), "-scale-to", "2000", "-png", p, prefix); err != nil {
				return Result{}, err
			}
			images, err = filepath.Glob(prefix + "-*.png")
			if err != nil || len(images) != pages {
				return Result{}, errors.New("document rendering failed")
			}
		}
		prepared := make([]string, 0, len(images)*2)
		crops := 0
		for _, p := range images {
			cards, err := prepareCards(ctx, p)
			if err != nil {
				return Result{}, err
			}
			if len(cards) == 0 {
				prepared = append(prepared, p)
			} else {
				prepared = append(prepared, cards...)
				crops += len(cards)
				for _, card := range cards {
					cardSources[card] = true
				}
			}
		}
		e.Service.opt.Log.Info("OCR document prepared", "job_id", j.ID, "document_slot", i, "kind", meta.Purpose, "mime", meta.Mime, "pages", len(images), "card_crops", crops)
		for _, image := range prepared {
			sources = append(sources, image)
			if e.Recognizer != "" {
				continue
			}
			b, err := run(ctx, dir, "tesseract", image, "stdout", "-l", "kaz+rus+eng", "--oem", "1", "-c", "tessedit_create_tsv=1")
			if err != nil {
				return Result{}, err
			}
			found, err := ParseTSV(bytes.NewReader(b))
			if err != nil {
				return Result{}, &processingError{Stage: "tesseract", Code: "output_invalid"}
			}
			for i := range found {
				found[i].FromCard = cardSources[image]
			}
			lines = append(lines, found...)
		}
	}
	if e.Recognizer != "" {
		// Image decoding/PNG preparation is finished. Return its transient heap
		// before starting ONNX so both processes fit the shared container limit.
		debug.FreeOSMemory()
		lines, err = e.recognize(ctx, dir, sources, cardSources)
		if err != nil {
			return Result{}, err
		}
	}
	e.Service.opt.Log.Info("OCR documents read", "job_id", j.ID, "unique_documents", len(seen), "duplicate_slots", duplicates, "images", len(sources), "lines", len(lines))
	r := Extract(lines)
	if e.Recognizer != "" {
		r.Engine = "pp-ocrv5-cyrillic-onnx"
	}
	// Sparse-text segmentation is a single bounded fallback for photographed
	// cards/screenshots. It contributes evidence; it cannot erase conflicts or
	// increase the confidence of an already uncertain extraction.
	if e.Recognizer == "" && incomplete(r, e.Service.opt.MinConfidence) {
		var sparse []Line
		for _, p := range sources {
			b, err := run(ctx, dir, "tesseract", p, "stdout", "-l", "kaz+rus+eng", "--oem", "1", "--psm", "11", "-c", "tessedit_create_tsv=1")
			if err != nil {
				return Result{}, err
			}
			found, err := ParseTSV(bytes.NewReader(b))
			if err != nil {
				return Result{}, &processingError{Stage: "tesseract", Code: "output_invalid"}
			}
			for i := range found {
				found[i].FromCard = cardSources[p]
			}
			sparse = append(sparse, found...)
		}
		r = MergeEvidence(r, Extract(sparse))
		r.Engine = "tesseract-kaz-rus-eng+psm11"
	}
	if len(embedded) > 0 {
		r.Engine = "pdftotext+" + r.Engine
		text := Extract(embedded)
		for _, pair := range []struct{ name, a, b string }{{"iin", r.Extracted.IIN, text.Extracted.IIN}, {"document_number", r.Extracted.DocumentNumber, text.Extracted.DocumentNumber}, {"first_name", r.FirstName, text.FirstName}, {"last_name", r.LastName, text.LastName}, {"document_type", r.DocumentType, text.DocumentType}} {
			if pair.a != "" && pair.b != "" && NormalizeName(pair.a) != NormalizeName(pair.b) {
				r.Reasons = append(r.Reasons, "pdf_text_conflict_"+pair.name)
			}
		}
	}
	return r, nil
}

func (e LocalEngine) recognize(ctx context.Context, dir string, sources []string, cards map[string]bool) ([]Line, error) {
	args := append([]string{e.Recognizer}, sources...)
	b, err := run(ctx, dir, "python3", args...)
	if err != nil {
		return nil, err
	}
	var pages [][]Line
	if err = json.Unmarshal(b, &pages); err != nil || len(pages) != len(sources) {
		return nil, &processingError{Stage: "recognizer", Code: "output_invalid"}
	}
	var lines []Line
	for i, page := range pages {
		if len(page) > 1000 {
			return nil, &processingError{Stage: "recognizer", Code: "output_limit"}
		}
		for _, line := range page {
			if len(line.Text) > 4096 {
				return nil, &processingError{Stage: "recognizer", Code: "output_limit"}
			}
			line.FromCard = cards[sources[i]]
			lines = append(lines, line)
		}
	}
	return lines, nil
}
func incomplete(r Result, min float64) bool {
	if r.DocumentType == "" || r.FirstName == "" || r.LastName == "" || !r.Extracted.Complete() {
		return true
	}
	for _, key := range []string{"document_type", "first_name", "last_name", "iin", "document_number"} {
		if r.Fields[key] < min {
			return true
		}
	}
	return false
}
