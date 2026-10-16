package reports

import (
	"encoding/csv"
	"io"
	"net/http"
	"strconv"
	"time"
)

// utf8BOM makes spreadsheet applications detect UTF-8 (Kazakh and Russian text).
const utf8BOM = "\xEF\xBB\xBF"

// flushEvery bounds how many rows are buffered before they are sent.
const flushEvery = 200

// Cell is one CSV field. Text cells are guarded against formula injection;
// numbers and timestamps produced by the server are written as they are.
type Cell struct {
	value string
}

// Text is a field holding text that may come from users.
func Text(s string) Cell { return Cell{value: EscapeFormula(s)} }

// TextPtr is Text for an optional value (empty when nil).
func TextPtr(s *string) Cell {
	if s == nil {
		return Cell{}
	}
	return Text(*s)
}

// Int is an integer field.
func Int(n int64) Cell { return Cell{value: strconv.FormatInt(n, 10)} }

// IntPtr is Int for an optional value (empty when nil).
func IntPtr(n *int64) Cell {
	if n == nil {
		return Cell{}
	}
	return Int(*n)
}

// Time is an RFC 3339 timestamp in loc (empty when nil).
func Time(t *time.Time, loc *time.Location) Cell {
	if t == nil {
		return Cell{}
	}
	return Cell{value: t.In(loc).Format(time.RFC3339)}
}

// EscapeFormula prefixes a single quote to text that a spreadsheet would
// otherwise evaluate as a formula (leading = + - @, tab or carriage return).
func EscapeFormula(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// CSVWriter streams an RFC 4180 CSV document (CRLF line endings, quoting by
// encoding/csv) with a UTF-8 byte-order mark.
type CSVWriter struct {
	w     *csv.Writer
	flush func()
	rows  int
	cells []string
}

// NewCSVWriter writes the BOM and the header row to out.
func NewCSVWriter(out io.Writer, header ...string) (*CSVWriter, error) {
	if _, err := io.WriteString(out, utf8BOM); err != nil {
		return nil, err
	}
	cw := &CSVWriter{w: csv.NewWriter(out), flush: func() {}}
	cw.w.UseCRLF = true
	if f, ok := out.(http.Flusher); ok {
		cw.flush = f.Flush
	}
	if err := cw.w.Write(header); err != nil {
		return nil, err
	}
	return cw, nil
}

// Row writes one record.
func (c *CSVWriter) Row(cells ...Cell) error {
	c.cells = c.cells[:0]
	for _, cell := range cells {
		c.cells = append(c.cells, cell.value)
	}
	if err := c.w.Write(c.cells); err != nil {
		return err
	}
	c.rows++
	if c.rows%flushEvery == 0 {
		c.w.Flush()
		c.flush()
		return c.w.Error()
	}
	return nil
}

// Close flushes buffered rows.
func (c *CSVWriter) Close() error {
	c.w.Flush()
	c.flush()
	return c.w.Error()
}

// Rows is the number of data rows written.
func (c *CSVWriter) Rows() int { return c.rows }

// startCSV sets the download headers.
func startCSV(w http.ResponseWriter, filename string) {
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
}
