package receipt

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Labels as printed on Kaspi receipts, lower case. The Russian ones are
// calibrated on real receipts; the Kazakh ones follow Kaspi's Kazakh
// interface wording and are not calibrated yet (ARCHITECTURE §6.1).
var (
	serialLabels = []string{"№ чека", "номер чека", "чек №", "№ чек", "чек нөмірі", "чектің нөмірі", "түбіртек №", "түбіртек нөмірі"}
	binLabels    = []string{"иин/бин продавца", "бин/иин продавца", "бин продавца", "иин продавца",
		"сатушының жсн/бсн", "сатушының бсн/жсн", "сатушы жсн/бсн", "сатушының бсн", "сатушының жсн"}
	payerLabels = []string{"фио покупателя", "покупатель", "сатып алушының аты-жөні", "сатып алушының атыжөні",
		"сатып алушының т.а.ә.", "сатып алушы"}
	dateLabels    = []string{"дата и время", "күні мен уақыты", "күні және уақыты", "төлем күні мен уақыты"}
	methodLabels  = []string{"оплачено", "төленді", "способ оплаты", "төлем тәсілі"}
	addressLabels = []string{"адрес", "мекенжай", "мекенжайы"}
	otherLabels   = []string{"по астане", "астана уақыты бойынша", "чек не является фискальным", "чек фискалдық емес"}

	statusRU = []string{"оплата совершена", "оплата прошла успешно"}
	statusKK = []string{"төлем жасалды", "төлем сәтті өтті", "төлем өтті", "төлем сәтті жасалды"}
)

var (
	serialRe   = regexp.MustCompile(`^[A-Z0-9]{4,40}$`)
	kaspiSerRe = regexp.MustCompile(`^QR[0-9]{8,20}$`)
	anySerRe   = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(QR ?[0-9]{8,20})(?:$|[^0-9])`)
	amountRe   = regexp.MustCompile(`^([0-9]{1,3}(?: [0-9]{3})+|[0-9]+)(?:[.,]([0-9]{1,2}))?$`)
	dateRe     = regexp.MustCompile(`([0-9]{2})\.([0-9]{2})\.([0-9]{4})[ ,]+([0-9]{1,2}):([0-9]{2})(?::([0-9]{2}))?`)
	binRe      = regexp.MustCompile(`^[0-9]{12}$`)
)

// cleanItem normalises one text run: NFC, every kind of space as a plain
// space, runs of white space collapsed, trimmed.
func cleanItem(s string) string {
	s = norm.NFC.String(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case r == ' ' || r == ' ' || r == ' ' || r == ' ' || r == ' ' || r == ' ':
			return ' '
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r) || r == '�':
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func labelKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimSpace(strings.TrimSuffix(s, ":"))
}

func matchesAny(s string, labels []string, prefix bool) bool {
	k := labelKey(s)
	for _, l := range labels {
		if k == l || (prefix && strings.HasPrefix(k, l+" ")) {
			return true
		}
	}
	return false
}

func isLabel(s string) bool {
	return matchesAny(s, serialLabels, false) || matchesAny(s, binLabels, false) ||
		matchesAny(s, payerLabels, false) || matchesAny(s, dateLabels, true) ||
		matchesAny(s, methodLabels, false) || matchesAny(s, addressLabels, false) ||
		matchesAny(s, otherLabels, true)
}

func isStatus(s string) bool { return matchesAny(s, statusRU, false) || matchesAny(s, statusKK, false) }

// valueAfter returns the first non-label item after index i.
func valueAfter(items []string, i int) (string, int) {
	for j := i + 1; j < len(items) && j <= i+3; j++ {
		if isLabel(items[j]) {
			continue
		}
		return items[j], j
	}
	return "", -1
}

func labelIndex(items []string, labels []string, prefix bool) int {
	for i, it := range items {
		if matchesAny(it, labels, prefix) {
			return i
		}
	}
	return -1
}

// normalizeSerial upper-cases a receipt number and removes spaces.
func normalizeSerial(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// findSerials returns the distinct Kaspi receipt numbers ("QR" + digits) in s.
func findSerials(s string) []string {
	var out []string
	for _, m := range anySerRe.FindAllStringSubmatch(s, 8) {
		v := normalizeSerial(m[1])
		dup := false
		for _, o := range out {
			dup = dup || o == v
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out
}

// extractFields fills the receipt fields from the text items in content order.
func extractFields(items []string, r *Receipt, flags map[string]bool) {
	firstLabel := len(items)
	for i, it := range items {
		if isLabel(it) {
			firstLabel = i
			break
		}
	}

	// Status line.
	switch {
	case containsItem(items, statusRU):
	case containsItem(items, statusKK):
		flags[FlagStatusKazakh] = true
	default:
		flags[FlagStatusMissing] = true
	}

	// Receipt number: the value after "№ чека"; every QR number in the text
	// must be that one.
	var found []string
	for _, it := range items {
		for _, s := range findSerials(it) {
			if !containsString(found, s) {
				found = append(found, s)
			}
		}
	}
	if i := labelIndex(items, serialLabels, false); i >= 0 {
		if v, _ := valueAfter(items, i); v != "" {
			if s := normalizeSerial(v); serialRe.MatchString(s) {
				r.Serial = s
			}
		}
	}
	switch {
	case r.Serial != "":
		for _, s := range found {
			if s != r.Serial {
				flags[FlagSerialAmbiguous] = true
			}
		}
	case len(found) == 1:
		r.Serial = found[0]
		flags[FlagSerialLabelMissing] = true
	case len(found) > 1:
		flags[FlagSerialAmbiguous] = true
	}
	if r.Serial != "" && !kaspiSerRe.MatchString(r.Serial) {
		flags[FlagSerialFormat] = true
	}

	// Amount: the number shown with "₸" above the first label.
	amountIdx := -1
	var amounts []int64
	for i := 0; i < firstLabel; i++ {
		it := items[i]
		if !strings.Contains(it, "₸") {
			continue
		}
		num := strings.TrimSpace(strings.Replace(it, "₸", "", 1))
		if num == "" && i > 0 {
			num = items[i-1]
		}
		if a, frac, ok := parseAmount(num); ok {
			if frac {
				flags[FlagAmountFractional] = true
			}
			if !containsAmount(amounts, a) {
				amounts = append(amounts, a)
			}
			if amountIdx < 0 {
				amountIdx = i
			}
		}
	}
	if len(amounts) > 0 {
		r.Amount = amounts[0]
	}
	if len(amounts) > 1 {
		flags[FlagAmountAmbiguous] = true
	}

	// Seller name: the line between the amount and the first label.
	for i := amountIdx + 1; amountIdx >= 0 && i < firstLabel; i++ {
		if it := items[i]; !isStatus(it) && !strings.Contains(it, "₸") && !isAmountLike(it) {
			r.RecipientName = clip(it, 200)
			break
		}
	}

	if i := labelIndex(items, binLabels, false); i >= 0 {
		if v, _ := valueAfter(items, i); v != "" {
			if b := strings.ReplaceAll(v, " ", ""); binRe.MatchString(b) {
				r.RecipientBIN = b
			}
		}
	}
	if r.RecipientBIN == "" {
		flags[FlagBINMissing] = true
	}

	if i := labelIndex(items, payerLabels, false); i >= 0 {
		if v, _ := valueAfter(items, i); v != "" {
			r.PayerMasked = MaskName(v)
		}
	}
	if i := labelIndex(items, methodLabels, false); i >= 0 {
		if v, _ := valueAfter(items, i); v != "" {
			r.PaymentMethod = clip(v, 100)
		}
	}

	// Payment time: the date after "Дата и время", else the only date.
	if i := labelIndex(items, dateLabels, true); i >= 0 {
		for j := i; j < len(items) && j <= i+3; j++ {
			if t, ok := parseDateTime(items[j]); ok {
				r.PaidAt = &t
				break
			}
		}
	}
	if r.PaidAt == nil {
		var dates []time.Time
		for _, it := range items {
			if t, ok := parseDateTime(it); ok {
				dates = append(dates, t)
			}
		}
		if len(dates) == 1 {
			r.PaidAt = &dates[0]
		}
	}
}

func containsItem(items, phrases []string) bool {
	for _, it := range items {
		if matchesAny(it, phrases, false) {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsAmount(list []int64, a int64) bool {
	for _, v := range list {
		if v == a {
			return true
		}
	}
	return false
}

func isAmountLike(s string) bool {
	_, _, ok := parseAmount(s)
	return ok
}

// maxAmount bounds a parsed amount (KZT).
const maxAmount = 100_000_000

// parseAmount reads "500", "5 000", "5 000,00" or "1 234.5". frac reports a
// non-zero fractional part (tiyn), which a top-up cannot have.
func parseAmount(s string) (amount int64, frac bool, ok bool) {
	s = strings.TrimSpace(s)
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(m[1], " ", ""), 10, 64)
	if err != nil || n <= 0 || n > maxAmount {
		return 0, false, false
	}
	return n, strings.Trim(m[2], "0") != "", true
}

// parseDateTime reads "dd.mm.yyyy HH:MM[:SS]" in Astana time.
func parseDateTime(s string) (time.Time, bool) {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	sec := 0
	if m[6] != "" {
		sec, _ = strconv.Atoi(m[6])
	}
	if year < 2020 || year > 2100 || month < 1 || month > 12 || hour > 23 || minute > 59 || sec > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, 0, Astana)
	if t.Day() != day || int(t.Month()) != month {
		return time.Time{}, false // 31.02 and the like
	}
	return t.UTC(), true
}

// MaskName keeps the first letter of every word ("Ерлан Б." → "Е*** Б.");
// initials stay as they are. At most three words are kept.
func MaskName(s string) string {
	words := strings.Fields(s)
	if len(words) > 3 {
		words = words[:3]
	}
	for i, w := range words {
		r := []rune(w)
		switch {
		case len(r) <= 2 && (len(r) == 1 || r[1] == '.'):
			// an initial such as "Б." stays readable
		default:
			words[i] = string(r[0]) + "***"
		}
	}
	return clip(strings.Join(words, " "), 100)
}

// confusables maps Cyrillic letters that look like Latin ones.
var confusables = strings.NewReplacer(
	"А", "A", "В", "B", "Е", "E", "К", "K", "М", "M", "Н", "H", "О", "O", "Р", "P", "С", "C", "Т", "T",
	"Х", "X", "У", "Y", "І", "I", "Ј", "J", "Ѕ", "S", "Қ", "K", "Ү", "Y",
)

// legalForms are business-form words ignored when matching seller names,
// stored in the form SellerCore compares (upper case, look-alikes as Latin).
var legalForms = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range []string{"ИП", "IP", "IE", "ТОО", "ЖШС", "LLP", "АО", "АҚ", "JSC", "ЖК", "Индивидуальный",
		"Предприниматель", "Жеке", "Кәсіпкер"} {
		m[confusables.Replace(strings.ToUpper(w))] = true
	}
	return m
}()

// NameKey normalises a seller name for comparison: upper case, Cyrillic
// look-alikes as Latin, letters and digits only.
func NameKey(s string) string {
	s = confusables.Replace(strings.ToUpper(norm.NFC.String(s)))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SellerCore is the distinctive part of a configured seller name: its words
// without business forms, normalised by NameKey ("ИП \"JAN-TAXI\"" → "JANTAXI").
func SellerCore(expected string) string {
	words := strings.FieldsFunc(confusables.Replace(strings.ToUpper(expected)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var b strings.Builder
	for _, w := range words {
		if !legalForms[w] {
			b.WriteString(NameKey(w))
		}
	}
	return b.String()
}

// SellerMatches reports whether the printed seller name contains the
// distinctive part of the expected one.
func SellerMatches(printed, expected string) bool {
	core := SellerCore(expected)
	return len([]rune(core)) >= 3 && strings.Contains(NameKey(printed), core)
}
