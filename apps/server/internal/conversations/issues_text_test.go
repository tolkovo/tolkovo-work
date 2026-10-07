package conversations

import (
	"strings"
	"testing"
)

// A draft reads everything the customer wrote, not a greeting alone, and is
// titled by the message that says the most.
func TestCustomerText(t *testing.T) {
	text, headline := customerText([]string{"Версия 2.3.1, iPhone", "Приложение вылетает при входе в профиль", " Привет "})
	if text != "Привет\n\nПриложение вылетает при входе в профиль\n\nВерсия 2.3.1, iPhone" || headline != "Приложение вылетает при входе в профиль" {
		t.Fatalf("text %q, headline %q", text, headline)
	}
	// Too long: the newest messages stay.
	long := strings.Repeat("я", MaxBodyLength-10)
	text, _ = customerText([]string{"последнее", long, "первое"})
	if text != "последнее" {
		t.Fatalf("kept the wrong messages: %d characters", len([]rune(text)))
	}
	if text, headline = customerText(nil); text != "" || headline != "" {
		t.Fatalf("empty: %q %q", text, headline)
	}
}
