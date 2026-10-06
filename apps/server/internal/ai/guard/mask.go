// Package guard keeps personal data away from language models (docs/03
// §10.2). Before a request leaves for a provider, Masker replaces email
// addresses, phone numbers, payment cards with their CVV, SNILS, INN and
// Russian passport numbers with tokens like ⟨PHONE_1⟩. The table of tokens
// lives in the Masker, in memory, for one call; Unmask puts the values back
// into the answer, except cards and CVV, which never come back.
//
// Detection is regular expressions plus checksums (Luhn for cards, the
// SNILS and INN check digits), tuned to mask too much rather than too
// little: a ten-digit order number may come out as a phone.
package guard

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind of personal data.
type Kind string

// Kinds, as they appear in tokens.
const (
	Email    Kind = "EMAIL"
	Phone    Kind = "PHONE"
	Card     Kind = "CARD"
	CVV      Kind = "CVV"
	SNILS    Kind = "SNILS"
	INN      Kind = "INN"
	Passport Kind = "PASSPORT"
)

var (
	// emailRE: internationalized addresses too (иван@пример.рф).
	emailRE = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+@[\p{L}\p{N}\-]+(?:\.[\p{L}\p{N}\-]+)*\.\p{L}{2,}`)
	// cvvRE: the code after its label; only the digits are replaced.
	cvvRE = regexp.MustCompile(`(?i)\b(cvv2?|cvc2?|cid)(\W{0,3})(\d{3,4})\b`)
	// passportRE: series of four digits (often split in two) and a
	// six-digit number, after a word like "паспорт".
	passportRE = regexp.MustCompile(`(?i)(паспорт\S*|passport)(\D{0,20}?)(\d{2}\s?\d{2}\s?№?\s?\d{6})\b`)
	// seriesRE: the same written as "серия 4512 номер 123456".
	seriesRE = regexp.MustCompile(`(?i)(сери[яи])(\s*)(\d{2}\s?\d{2}\s*(?:номер|№)\s*\d{6})\b`)
	// cvvTailRE: three or four digits right after a card number, a CVV
	// written without its label.
	cvvTailRE = regexp.MustCompile(`^(\s{1,2})(\d{3,4})$`)
	// digitsRE: a run of 10 or more digits with short separators, the shape
	// of phones, cards, SNILS and INN; classified by its digits. A longer
	// run is taken whole, so that a card is not cut where the run is.
	digitsRE = regexp.MustCompile(`\+?\(?\d(?:[ \-().]{0,2}\d){9,40}`)
	tokenRE  = regexp.MustCompile(`⟨([A-Z]+)_(\d+)⟩`)
)

// Masker masks one call's texts consistently: the same value gets the same
// token in every text it masks.
type Masker struct {
	values map[string]string // token → value
	tokens map[string]string // kind + value → token
	counts map[Kind]int
}

// NewMasker returns an empty Masker.
func NewMasker() *Masker {
	return &Masker{values: map[string]string{}, tokens: map[string]string{}, counts: map[Kind]int{}}
}

// Mask returns s with personal data replaced by tokens.
func (m *Masker) Mask(s string) string {
	s = emailRE.ReplaceAllStringFunc(s, func(v string) string { return m.token(Email, v) })
	s = cvvRE.ReplaceAllStringFunc(s, func(v string) string {
		g := cvvRE.FindStringSubmatch(v)
		return g[1] + g[2] + m.token(CVV, g[3])
	})
	s = passportRE.ReplaceAllStringFunc(s, func(v string) string {
		g := passportRE.FindStringSubmatch(v)
		return g[1] + g[2] + m.token(Passport, g[3])
	})
	s = seriesRE.ReplaceAllStringFunc(s, func(v string) string {
		g := seriesRE.FindStringSubmatch(v)
		return g[1] + g[2] + m.token(Passport, g[3])
	})
	return digitsRE.ReplaceAllStringFunc(s, m.maskRun)
}

// maskRun masks a run of digits: as a whole when it is one of the kinds,
// otherwise a card number inside it, such as one followed by its expiry
// month or its CVV.
func (m *Masker) maskRun(v string) string {
	if kind, ok := classify(v); ok {
		return m.token(kind, v)
	}
	i, j, ok := cardInside(v)
	if !ok {
		return v
	}
	out, rest := v[:i]+m.token(Card, v[i:j]), v[j:]
	if g := cvvTailRE.FindStringSubmatch(rest); g != nil {
		return out + g[1] + m.token(CVV, g[2])
	}
	return out + rest
}

// cardInside finds a card number within a run: 13 to 19 digits starting
// with 2–6, as card numbers do, passing Luhn, and starting and ending on
// the run's groups of digits (anywhere in a run without separators). It
// returns the byte range of the number in the run.
func cardInside(run string) (int, int, bool) {
	if strings.HasPrefix(run, "+") {
		return 0, 0, false
	}
	var pos []int     // byte offset of each digit
	var starts []bool // whether a digit opens a group
	for i, r := range run {
		if r >= '0' && r <= '9' {
			starts = append(starts, len(pos) == 0 || pos[len(pos)-1] != i-1)
			pos = append(pos, i)
		}
	}
	digits := make([]byte, len(pos))
	for k, p := range pos {
		digits[k] = run[p]
	}
	single := len(pos) == len(run)
	for _, n := range []int{16, 19, 18, 17, 15, 14, 13} {
		for k := 0; k+n <= len(pos); k++ {
			end := k + n
			if !starts[k] || (!single && end < len(pos) && !starts[end]) {
				continue
			}
			if c := digits[k]; c < '2' || c > '6' || !luhn(string(digits[k:end])) {
				continue
			}
			return pos[k], pos[end-1] + 1, true
		}
	}
	return 0, 0, false
}

// Unmask puts the masked values back into s; tokens of cards and CVV stay
// tokens, and unknown tokens are left as they are.
func (m *Masker) Unmask(s string) string {
	return tokenRE.ReplaceAllStringFunc(s, func(t string) string {
		g := tokenRE.FindStringSubmatch(t)
		if Kind(g[1]) == Card || Kind(g[1]) == CVV {
			return t
		}
		if v, ok := m.values[t]; ok {
			return v
		}
		return t
	})
}

// Counts reports how many distinct values of each kind were masked.
func (m *Masker) Counts() map[Kind]int {
	out := make(map[Kind]int, len(m.counts))
	for k, n := range m.counts {
		out[k] = n
	}
	return out
}

func (m *Masker) token(kind Kind, v string) string {
	key := string(kind) + "\x00" + v
	if t, ok := m.tokens[key]; ok {
		return t
	}
	m.counts[kind]++
	t := fmt.Sprintf("⟨%s_%d⟩", kind, m.counts[kind])
	m.tokens[key], m.values[t] = t, v
	return t
}

// classify names a run of digits, or reports it is none of them.
func classify(run string) (Kind, bool) {
	var d strings.Builder
	for _, r := range run {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	digits := d.String()
	plus := strings.HasPrefix(run, "+")
	switch n := len(digits); {
	case n >= 13 && n <= 19 && !plus && luhn(digits):
		return Card, true
	case n == 11 && !plus && snils(digits) && strings.ContainsAny(run, " -"):
		return SNILS, true // written as 123-456-789 01; plain 11 digits are rather a phone
	case (n == 10 || n == 12) && !plus && inn(digits) && !strings.ContainsAny(run, " -()."):
		return INN, true
	case n >= 10 && n <= 15 && (plus || n == 11 && (digits[0] == '7' || digits[0] == '8') || n == 10 && digits[0] == '9'):
		return Phone, true
	}
	return "", false
}

// luhn checks a card number.
func luhn(digits string) bool {
	sum := 0
	for i := range len(digits) {
		d := int(digits[len(digits)-1-i] - '0')
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// snils checks the two check digits of an 11-digit SNILS.
func snils(digits string) bool {
	sum := 0
	for i := range 9 {
		sum += int(digits[i]-'0') * (9 - i)
	}
	check := sum % 101
	if check == 100 {
		check = 0
	}
	return check == int(digits[9]-'0')*10+int(digits[10]-'0')
}

// inn checks the check digits of a 10-digit (organization) or 12-digit
// (person) INN.
func inn(digits string) bool {
	ctl := func(weights []int) int {
		sum := 0
		for i, w := range weights {
			sum += int(digits[i]-'0') * w
		}
		return sum % 11 % 10
	}
	switch len(digits) {
	case 10:
		return ctl([]int{2, 4, 10, 3, 5, 9, 4, 6, 8}) == int(digits[9]-'0')
	case 12:
		return ctl([]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}) == int(digits[10]-'0') &&
			ctl([]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}) == int(digits[11]-'0')
	}
	return false
}
