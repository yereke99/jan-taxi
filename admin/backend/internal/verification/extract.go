package verification

import (
	"bufio"
	"encoding/csv"
	"errors"
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
	Words      []Word
	FromCard   bool
	Conflict   bool
}

type Word struct {
	Text       string
	Confidence float64
}

// score measures the words that supplied a field, rather than unrelated words
// in the same bilingual label, barcode, or row. It never raises a word's score.
func (l Line) score(value string) float64 {
	if len(l.Words) == 0 {
		return l.Confidence
	}
	value = NormalizeName(value)
	if value == "" {
		return l.Confidence
	}
	text := NormalizeName(l.Text)
	score, found := 100.0, false
	for from := 0; from < len(text); {
		start := strings.Index(text[from:], value)
		if start < 0 {
			break
		}
		start += from
		end, pos := start+len(value), 0
		for _, word := range l.Words {
			length := len(NormalizeName(word.Text))
			if pos < end && pos+length > start {
				score, found = min(score, word.Confidence), true
			}
			pos += length + 1
		}
		from = end
	}
	if found {
		return score
	}
	return l.Confidence
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
	header, err := cr.Read()
	if err != nil || len(header) < 12 || header[0] != "level" || header[10] != "conf" || header[11] != "text" {
		return nil, errors.New("invalid OCR TSV header")
	}
	var lines []Line
	key := ""
	var text []string
	var words []Word
	conf := 100.0
	flush := func() {
		if len(text) > 0 {
			lines = append(lines, Line{Text: strings.Join(text, " "), Confidence: conf, Words: words})
		}
		text = nil
		words = nil
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
		words = append(words, Word{Text: word, Confidence: c})
		if c < conf {
			conf = c
		}
	}
	flush()
	return lines, nil
}

var iinRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]{12})(?:$|[^0-9])`)
var licenceNameRE = regexp.MustCompile(`^([12])[.)]\s+(.+)$`)
var licenceNumberRE = regexp.MustCompile(`^5[.)]\s+(.+)$`)
var numericRunRE = regexp.MustCompile(`[0-9]+`)

func exactNumbers(text string, size int) []string {
	var out []string
	for _, v := range numericRunRE.FindAllString(text, -1) {
		if len(v) == size {
			out = append(out, v)
		}
	}
	return out
}

func compactSpaces(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

func (l Line) numericScore(value string) float64 {
	best, found := 100.0, false
	for i := range l.Words {
		joined, score := "", 100.0
		for _, word := range l.Words[i:] {
			if strings.Trim(word.Text, "0123456789") != "" {
				break
			}
			joined += word.Text
			score = min(score, word.Confidence)
			if joined == value {
				best, found = min(best, score), true
				break
			}
			if len(joined) >= len(value) {
				break
			}
		}
	}
	if found {
		return best
	}
	return l.score(value)
}

var personalLabels = []struct {
	field string
	re    *regexp.Regexp
}{
	{"patronymic", regexp.MustCompile(`(?:^|[ /|:])(?:ОТЧЕСТВО|ӘКЕСІНІҢ АТЫ)`)},
	{"iin", regexp.MustCompile(`(?:^|[ /|:])(?:ЖСН|ИИН|IIN)`)},
	{"last_name", regexp.MustCompile(`(?:^|[ /|:])(?:ФАМИЛИЯ|ТЕГІ)`)},
	{"first_name", regexp.MustCompile(`(?:^|[ /|:])(?:ИМЯ|АТЫ)`)},
}

func personalField(t string) (string, string) {
	for _, label := range personalLabels {
		end := -1
		for _, match := range label.re.FindAllStringIndex(t, -1) {
			if match[1] == len(t) || strings.ContainsRune(" /|:", rune(t[match[1]])) || (label.field == "iin" && t[match[1]] >= '0' && t[match[1]] <= '9') {
				end = match[1]
			}
		}
		if end >= 0 {
			return label.field, strings.Trim(t[end:], " /|:")
		}
	}
	return "", ""
}

func Extract(lines []Line) Result {
	r := Result{Fields: map[string]float64{}, Comparisons: map[string]string{}, Reasons: []string{}, Engine: "tesseract-kaz-rus-eng"}
	text := make([]string, 0, len(lines))
	for _, l := range lines {
		if l.Conflict {
			r.Reasons = append(r.Reasons, "conflicting_ocr_reading")
		}
		text = append(text, NormalizeName(l.Text))
	}
	all := strings.Join(text, " ")
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
		for _, title := range []string{"ЖЕКЕ КУӘЛІК", "УДОСТОВЕРЕНИЕ ЛИЧНОСТИ", "DRIVING LICENCE", "ВОДИТЕЛЬСКОЕ УДОСТОВЕРЕНИЕ", "ЖҮРГІЗУШІ КУӘЛІГІ"} {
			if !strings.Contains(t, title) {
				continue
			}
			c := l.score(title)
			if old, ok := r.Fields["document_type"]; !ok || c < old {
				r.Fields["document_type"] = c
			}
		}
	}
	// Detectors may put the two words of a title into adjacent boxes. Join only
	// when no complete title had its own score, keeping the actual scores.
	if _, ok := r.Fields["document_type"]; !ok && r.DocumentType != "" {
		for i := 0; i+1 < len(lines); i++ {
			joined := NormalizeName(lines[i].Text + " " + lines[i+1].Text)
			for _, title := range []string{"ЖЕКЕ КУӘЛІК", "УДОСТОВЕРЕНИЕ ЛИЧНОСТИ", "DRIVING LICENCE", "ВОДИТЕЛЬСКОЕ УДОСТОВЕРЕНИЕ", "ЖҮРГІЗУШІ КУӘЛІГІ"} {
				if strings.Contains(joined, title) {
					c := min(lines[i].Confidence, lines[i+1].Confidence)
					if old, ok := r.Fields["document_type"]; !ok || c < old {
						r.Fields["document_type"] = c
					}
				}
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
						if r.LastName != "" && r.LastName != val {
							r.Reasons = append(r.Reasons, "conflicting_last_name")
							continue
						}
						r.LastName = val
						c := l.score(val)
						if old, ok := r.Fields["last_name"]; !ok || c < old {
							r.Fields["last_name"] = c
						}
					} else {
						if r.FirstName != "" && r.FirstName != val {
							r.Reasons = append(r.Reasons, "conflicting_first_name")
							continue
						}
						r.FirstName = val
						c := l.score(val)
						if old, ok := r.Fields["first_name"]; !ok || c < old {
							r.Fields["first_name"] = c
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
						c := l.score(strings.TrimSpace(m[1]))
						if old, ok := r.Fields["document_number"]; !ok || c < old {
							r.Fields["document_number"] = c
						}
					}
				}
				continue
			}
		}
		field, inline := personalField(t)
		if field == "" {
			continue
		}
		val := inline
		c := l.Confidence
		if field == "iin" {
			candidates := exactNumbers(t, 12)
			if len(candidates) > 1 {
				for _, candidate := range candidates[1:] {
					if candidate != candidates[0] {
						r.Reasons = append(r.Reasons, "conflicting_iin")
					}
				}
			}
			if m := iinRE.FindStringSubmatch(t); len(m) > 1 {
				val = m[1]
			}
		}
		if val != "" {
			c = l.score(val)
		}
		if val == "" && i+1 < len(lines) {
			candidate := strings.TrimSpace(lines[i+1].Text)
			if !isLabel(candidate) {
				val = candidate
				c = lines[i+1].score(candidate)
			}
		}
		if field == "iin" {
			values := exactNumbers(compactSpaces(val), 12)
			if len(values) == 0 {
				continue
			}
			val = values[0]
			for _, other := range values[1:] {
				if other != val {
					r.Reasons = append(r.Reasons, "conflicting_iin")
				}
			}
			c = l.numericScore(val)
			if !strings.Contains(compactSpaces(l.Text), val) && i+1 < len(lines) {
				c = lines[i+1].numericScore(val)
			}
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
			// A complete twelve-digit run inside a detected card can only be
			// the IIN in these layouts. Viewer filenames are outside these crops.
			// Do not repair digits, guess from MRZ, or accept ambiguous candidates.
			if l.FromCard {
				for _, v := range exactNumbers(l.Text, 12) {
					if !ValidIIN(v) {
						continue
					}
					if r.Extracted.IIN != "" && r.Extracted.IIN != v {
						r.Reasons = append(r.Reasons, "conflicting_iin")
						continue
					}
					r.Extracted.IIN = v
					c := l.numericScore(v)
					if old, ok := r.Fields["iin"]; !ok || c < old {
						r.Fields["iin"] = c
					}
				}
			}
			for _, v := range exactNumbers(l.Text, 9) {
				if r.Extracted.DocumentNumber != "" && r.Extracted.DocumentNumber != v {
					r.Reasons = append(r.Reasons, "conflicting_document_number")
					continue
				}
				r.Extracted.DocumentNumber = v
				c := l.score(v)
				if old, ok := r.Fields["document_number"]; !ok || c < old {
					r.Fields["document_number"] = c
				}
			}
		}
	}
	return r
}
func isLabel(s string) bool {
	s = NormalizeName(s)
	if field, _ := personalField(s); field != "" {
		return true
	}
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
	// Licence field 2 may contain both given name and patronymic. Split the
	// recognized string only when the entire entered pair matches it exactly.
	// No characters are inferred from a partial name or transliterated.
	if r.DocumentType == "driving_licence" && want.Patronymic != "" && r.Extracted.Patronymic == "" && NormalizeName(r.FirstName) == NormalizeName(want.FirstName+" "+want.Patronymic) {
		full := NormalizeName(r.FirstName)
		split := len(NormalizeName(want.FirstName))
		r.FirstName, r.Extracted.Patronymic = full[:split], full[split+1:]
		r.Fields["patronymic"] = r.Fields["first_name"]
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
