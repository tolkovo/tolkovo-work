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
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	// cvvRE: the code after its label; only the digits are replaced.
	cvvRE = regexp.MustCompile(`(?i)\b(cvv2?|cvc2?|cid)(\W{0,3})(\d{3,4})\b`)
	// passportRE: series of four digits (often split in two) and a
	// six-digit number, after a word like "паспорт".
	passportRE = regexp.MustCompile(`(?i)(паспорт\S*|passport)(\D{0,20}?)(\d{2}\s?\d{2}\s?№?\s?\d{6})\b`)
	// digitsRE: a run of 10 to 19 digits with short separators, the shape
	// of phones, cards, SNILS and INN; classified by its digits.
	digitsRE = regexp.MustCompile(`\+?\(?\d(?:[ \-().]{0,2}\d){9,18}`)
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
	return digitsRE.ReplaceAllStringFunc(s, func(v string) string {
		kind, ok := classify(v)
		if !ok {
			return v
		}
		return m.token(kind, v)
	})
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
