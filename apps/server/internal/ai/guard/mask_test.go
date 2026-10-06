package guard

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"email", "пишите на anna.k+shop@mail.example.ru, спасибо", "пишите на ⟨EMAIL_1⟩, спасибо"},
		{"phone +7 with brackets", "мой номер +7 (916) 123-45-67.", "мой номер ⟨PHONE_1⟩."},
		{"phone 8", "звоните 8 916 123 45 67", "звоните ⟨PHONE_1⟩"},
		{"phone without prefix", "9162345678 — это я", "⟨PHONE_1⟩ — это я"},
		{"international phone", "call +44 20 7946 0958 please", "call ⟨PHONE_1⟩ please"},
		{"card with spaces", "карта 4111 1111 1111 1111 не проходит", "карта ⟨CARD_1⟩ не проходит"},
		{"card and cvv", "4242424242424242, CVV: 123", "⟨CARD_1⟩, CVV: ⟨CVV_1⟩"},
		{"snils", "СНИЛС 112-233-445 95", "СНИЛС ⟨SNILS_1⟩"},
		{"inn of a person", "ИНН 500100732259", "ИНН ⟨INN_1⟩"},
		{"inn of an organization", "ИНН 7707083893", "ИНН ⟨INN_1⟩"},
		{"passport", "паспорт 45 06 123456 выдан", "паспорт ⟨PASSPORT_1⟩ выдан"},
		{"passport with №", "Паспорт серии 4506 № 123456", "Паспорт серии ⟨PASSPORT_1⟩"},
		{"card and its expiry", "card 4111 1111 1111 1111 12/26", "card ⟨CARD_1⟩ 12/26"},
		{"card and a cvv without its label", "4111 1111 1111 1111 123 спасибо", "⟨CARD_1⟩ ⟨CVV_1⟩ спасибо"},
		{"card and its expiry, no spaces", "4111111111111111 0526", "⟨CARD_1⟩ ⟨CVV_1⟩"},
		{"card in a longer run", "41111111111111111226", "⟨CARD_1⟩1226"},
		{"internationalized email", "почта иван@пример.рф", "почта ⟨EMAIL_1⟩"},
		{"passport as series and number", "серия 4512 номер 123456", "серия ⟨PASSPORT_1⟩"},
		{"same value, same token", "a@b.io и снова a@b.io, и c@d.io", "⟨EMAIL_1⟩ и снова ⟨EMAIL_1⟩, и ⟨EMAIL_2⟩"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewMasker().Mask(tc.in); got != tc.want {
				t.Fatalf("Mask(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// What a crash report is full of stays as it is.
func TestMask_LeavesOrdinaryNumbers(t *testing.T) {
	for _, in := range []string{
		"version 2.14.0 (build 20261006), macOS 15.1",
		"2026-10-06 12:00:31 crashed at 0x00007ff8",
		"order 12345, 3 items, total 1 299,00 ₽",
		"card 4111 1111 1111 1112 fails Luhn and is too long for a phone",
		"error code 1234567890123",     // 13 digits, not Luhn, no plus
		"trace id 1759752031000123456", // no card number starts with 1
		"build 7 1111 1111 1111 1117",  // 7 is no card number's first digit
	} {
		if got := NewMasker().Mask(in); got != in {
			t.Errorf("Mask(%q) = %q", in, got)
		}
	}
	// A wrong check digit is not an INN; ten digits starting with 1 are no phone.
	if got := NewMasker().Mask("ИНН 1234567890"); got != "ИНН 1234567890" {
		t.Errorf("got %q", got)
	}
}

func TestUnmask(t *testing.T) {
	m := NewMasker()
	masked := m.Mask("Анна, anna@example.com, +7 916 123-45-67, карта 4111111111111111")
	if strings.Contains(masked, "anna@") || strings.Contains(masked, "916") || strings.Contains(masked, "4111") {
		t.Fatalf("not masked: %q", masked)
	}
	answer := "Ответим на ⟨EMAIL_1⟩ или по ⟨PHONE_1⟩; карту ⟨CARD_1⟩ не просим; ⟨EMAIL_9⟩ нам неизвестен"
	want := "Ответим на anna@example.com или по +7 916 123-45-67; карту ⟨CARD_1⟩ не просим; ⟨EMAIL_9⟩ нам неизвестен"
	if got := m.Unmask(answer); got != want {
		t.Fatalf("Unmask\n got %q\nwant %q", got, want)
	}
	if c := m.Counts(); c[Email] != 1 || c[Phone] != 1 || c[Card] != 1 {
		t.Fatalf("counts %v", c)
	}
}
