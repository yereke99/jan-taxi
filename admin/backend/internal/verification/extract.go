package verification

import (
	"bufio"
	"encoding/csv"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type Line struct {
	Text       string
	Confidence float64
}
type Result struct {
	Extracted    Input              `json:"extracted"`
	FirstName    string             `json:"first_name"`
	LastName     string             `json:"last_name"`
	DocumentType string             `json:"document_type"`
	Confidence   float64            `json:"confidence"`
	Fields       map[string]float64 `json:"field_confidence"`
	Comparisons  map[string]string  `json:"comparisons"`
	Reasons      []string           `json:"reasons"`
	Engine       string             `json:"engine"`
}

// ParseTSV keeps only word confidence and text, grouping by page/block/line.
// Output from the subprocess is capped before it reaches this parser.
func ParseTSV(r io.Reader) ([]Line, error) {
	cr := csv.NewReader(bufio.NewReader(r))
	cr.Comma = '\t'
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	var lines []Line
	key := ""
	var text []string
	conf := 100.0
	flush := func() {
		if len(text) > 0 {
			lines = append(lines, Line{strings.Join(text, " "), conf})
		}
		text = nil
		conf = 100
	}
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(row) < 12 || row[0] != "5" {
			continue
		}
		c, err := strconv.ParseFloat(row[10], 64)
		if err != nil || math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 100 {
			continue
		}
		word := strings.TrimSpace(strings.Join(row[11:], "\t"))
		if word == "" {
			continue
		}
		next := strings.Join(row[1:5], "/")
		if next != key {
			flush()
			key = next
		}
		text = append(text, word)
		if c < conf {
			conf = c
		}
	}
	flush()
	return lines, nil
}

var iinRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]{12})(?:$|[^0-9])`)
var docRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]{9})(?:$|[^0-9])`)
var licenceNameRE = regexp.MustCompile(`^([12])[.)]\s+(.+)$`)
var licenceNumberRE = regexp.MustCompile(`^5[.)]\s+(.+)$`)

func Extract(lines []Line) Result {
	r := Result{Fields: map[string]float64{}, Comparisons: map[string]string{}, Reasons: []string{}, Engine: "tesseract-kaz-rus-eng"}
	text := make([]string, 0, len(lines))
	for _, l := range lines {
		text = append(text, NormalizeName(l.Text))
	}
	all := strings.Join(text, "\n")
	if strings.Contains(all, "ЖЕКЕ КУӘЛІК") || strings.Contains(all, "УДОСТОВЕРЕНИЕ ЛИЧНОСТИ") {
		r.DocumentType = "id_card"
	}
	if strings.Contains(all, "DRIVING LICENCE") || strings.Contains(all, "ВОДИТЕЛЬСКОЕ УДОСТОВЕРЕНИЕ") || strings.Contains(all, "ЖҮРГІЗУШІ КУӘЛІГІ") {
		if r.DocumentType != "" {
			r.Reasons = append(r.Reasons, "conflicting_document_type")
		}
		r.DocumentType = "driving_licence"
	}
	for _, l := range lines {
		t := NormalizeName(l.Text)
		if strings.Contains(t, "ЖЕКЕ КУӘЛІК") || strings.Contains(t, "УДОСТОВЕРЕНИЕ ЛИЧНОСТИ") || strings.Contains(t, "DRIVING LICENCE") || strings.Contains(t, "ВОДИТЕЛЬСКОЕ УДОСТОВЕРЕНИЕ") || strings.Contains(t, "ЖҮРГІЗУШІ КУӘЛІГІ") {
			if old, ok := r.Fields["document_type"]; !ok || l.Confidence < old {
				r.Fields["document_type"] = l.Confidence
			}
		}
	}
	r.Extracted.DocumentType = r.DocumentType
	// Only labelled personal fields are extracted. Names/number in a viewer's
	// filename or an incomplete MRZ are never substituted for document fields.
	for i, l := range lines {
		t := text[i]
		field := ""
		if r.DocumentType == "driving_licence" {
			if m := licenceNameRE.FindStringSubmatch(t); len(m) > 2 {
				val := strings.TrimSpace(strings.Split(m[2], "/")[0])
				if nameValue(val) {
					if m[1] == "1" {
						r.LastName = val
						if old, ok := r.Fields["last_name"]; !ok || l.Confidence < old {
							r.Fields["last_name"] = l.Confidence
						}
					} else {
						r.FirstName = val
						if old, ok := r.Fields["first_name"]; !ok || l.Confidence < old {
							r.Fields["first_name"] = l.Confidence
						}
					}
				}
				continue
			}
			if m := licenceNumberRE.FindStringSubmatch(t); len(m) > 1 {
				v := NormalizeDocumentNumber(m[1])
				if validDocumentNumber("driving_licence", v) {
					if r.Extracted.DocumentNumber != "" && r.Extracted.DocumentNumber != v {
						r.Reasons = append(r.Reasons, "conflicting_document_number")
					} else {
						r.Extracted.DocumentNumber = v
						if old, ok := r.Fields["document_number"]; !ok || l.Confidence < old {
							r.Fields["document_number"] = l.Confidence
						}
					}
				}
				continue
			}
		}
		switch {
		case strings.Contains(t, "ЖСН") || strings.Contains(t, "ИИН") || strings.Contains(t, "IIN"):
			field = "iin"
		case strings.Contains(t, "ФАМИЛИЯ"):
			field = "last_name"
		case strings.Contains(t, "ОТЧЕСТВО"):
			field = "patronymic"
		case strings.Contains(t, "ИМЯ") && !strings.Contains(t, "ФАМИЛИЯ"):
			field = "first_name"
		}
		if field == "" {
			continue
		}
		val := ""
		c := l.Confidence
		if field == "iin" {
			if m := iinRE.FindStringSubmatch(t); len(m) > 1 {
				val = m[1]
			}
		}
		if val == "" && i+1 < len(lines) {
			candidate := strings.TrimSpace(lines[i+1].Text)
			if !isLabel(candidate) {
				val = candidate
				c = min(c, lines[i+1].Confidence)
			}
		}
		if field == "iin" {
			m := iinRE.FindStringSubmatch(val)
			if len(m) < 2 {
				continue
			}
			val = m[1]
		} else {
			val = NormalizeName(val)
			if !nameValue(val) {
				continue
			}
		}
		old := ""
		switch field {
		case "iin":
			old = r.Extracted.IIN
		case "first_name":
			old = r.FirstName
		case "last_name":
			old = r.LastName
		case "patronymic":
			old = r.Extracted.Patronymic
		}
		if old != "" && old != val {
			r.Reasons = append(r.Reasons, "conflicting_"+field)
			continue
		}
		switch field {
		case "iin":
			r.Extracted.IIN = val
		case "first_name":
			r.FirstName = val
		case "last_name":
			r.LastName = val
		case "patronymic":
			r.Extracted.Patronymic = val
		}
		if old, ok := r.Fields[field]; ok {
			c = min(c, old)
		}
		r.Fields[field] = c
	}
	// Kazakhstan ID-card number is nine digits. Require one unambiguous candidate
	// across both sides; MRZ numeric runs and viewer filenames don't qualify.
	if r.DocumentType == "id_card" {
		for _, l := range lines {
			for _, m := range docRE.FindAllStringSubmatch(l.Text, -1) {
				v := m[1]
				if r.Extracted.DocumentNumber != "" && r.Extracted.DocumentNumber != v {
					r.Reasons = append(r.Reasons, "conflicting_document_number")
					continue
				}
				r.Extracted.DocumentNumber = v
				if old, ok := r.Fields["document_number"]; !ok || l.Confidence < old {
					r.Fields["document_number"] = l.Confidence
				}
			}
		}
	}
	return r
}
func isLabel(s string) bool {
	s = NormalizeName(s)
	for _, v := range []string{"ФАМИЛИЯ", "ИМЯ", "ОТЧЕСТВО", "ТУҒАН", "РОЖДЕНИЯ", "ЖСН", "ИИН", "ЖЫНЫСЫ", "ДАТА", "БЕРІЛГЕН"} {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
func nameValue(s string) bool {
	if s == "" || len([]rune(s)) > 60 || isLabel(s) {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsMark(r) || strings.ContainsRune(" '-.", r)) {
			return false
		}
	}
	return true
}

// Assess never turns an uncertain extraction into a mismatch or a pass. Latin
// transliteration differences are reviewable, not guessed into equality.
func Assess(r Result, want Snapshot, min float64) Result {
	if r.Fields == nil {
		r.Fields = map[string]float64{}
	}
	r.Comparisons = map[string]string{}
	if r.Reasons == nil {
		r.Reasons = []string{}
	}
	if r.DocumentType != "id_card" && r.DocumentType != "driving_licence" {
		r.Reasons = append(r.Reasons, "unsupported_document")
	}
	if r.DocumentType != want.DocumentType {
		r.Reasons = append(r.Reasons, "document_type_mismatch")
	}
	r.Confidence = 100
	fields := []struct{ name, got, want string }{{"document_type", r.DocumentType, want.DocumentType}, {"first_name", r.FirstName, want.FirstName}, {"last_name", r.LastName, want.LastName}, {"iin", r.Extracted.IIN, want.IIN}, {"document_number", r.Extracted.DocumentNumber, want.DocumentNumber}}
	if want.Patronymic != "" {
		fields = append(fields, struct{ name, got, want string }{"patronymic", r.Extracted.Patronymic, want.Patronymic})
	}
	for _, f := range fields {
		c := r.Fields[f.name]
		if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 100 {
			c = 0
		}
		if c < r.Confidence {
			r.Confidence = c
		}
		state := "match"
		switch {
		case f.got == "" || f.want == "" || c < min:
			state = "uncertain"
		case NormalizeName(f.got) != NormalizeName(f.want):
			state = "mismatch"
			if (f.name == "first_name" || f.name == "last_name" || f.name == "patronymic") && differentScript(f.got, f.want) {
				state = "uncertain"
			}
		}
		r.Comparisons[f.name] = state
		if state != "match" {
			r.Reasons = append(r.Reasons, f.name+"_"+state)
		}
	}
	if !ValidIIN(want.IIN) || !ValidIIN(r.Extracted.IIN) {
		r.Reasons = append(r.Reasons, "invalid_iin")
	}
	if !validDocumentNumber(r.DocumentType, r.Extracted.DocumentNumber) {
		r.Reasons = append(r.Reasons, "invalid_document_number")
	}
	return r
}
func differentScript(a, b string) bool {
	cyr := func(s string) bool {
		for _, r := range s {
			if unicode.In(r, unicode.Cyrillic) {
				return true
			}
		}
		return false
	}
	return cyr(a) != cyr(b)
}

// MergeEvidence preserves conflicts and the lowest confidence for corroborated
// values. A second pass is never allowed to hide an uncertain first pass.
func MergeEvidence(a, b Result) Result {
	if a.Fields == nil {
		a.Fields = map[string]float64{}
	}
	merge := func(key string, left *string, right string) {
		if right == "" {
			return
		}
		if *left == "" {
			*left = right
			a.Fields[key] = b.Fields[key]
			return
		}
		if NormalizeName(*left) != NormalizeName(right) {
			a.Reasons = append(a.Reasons, "conflicting_"+key)
			return
		}
		a.Fields[key] = min(a.Fields[key], b.Fields[key])
	}
	merge("document_type", &a.DocumentType, b.DocumentType)
	merge("first_name", &a.FirstName, b.FirstName)
	merge("last_name", &a.LastName, b.LastName)
	merge("iin", &a.Extracted.IIN, b.Extracted.IIN)
	merge("document_number", &a.Extracted.DocumentNumber, b.Extracted.DocumentNumber)
	merge("patronymic", &a.Extracted.Patronymic, b.Extracted.Patronymic)
	a.Extracted.DocumentType = a.DocumentType
	a.Reasons = append(a.Reasons, b.Reasons...)
	return a
}
