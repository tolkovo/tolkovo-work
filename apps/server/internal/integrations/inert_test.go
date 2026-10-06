package integrations

import "testing"

// What the customer or the model wrote reaches GitHub as plain text.
func TestInert(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"после обновления @octocat и @org/team молчат", "после обновления `@octocat` и `@org/team` молчат"},
		{"как в #12, owner/repo#7 и GH-3", "как в `#12`, `owner/repo#7` и `GH-3`"},
		{"скрин ![экран](https://evil.example/p.png)", "скрин [экран](https://evil.example/p.png)"},
		{`<img src="https://evil.example/p.png">`, `&lt;img src="https://evil.example/p.png">`},
		// Emails, HTML entities and code spans stay as they are.
		{"пишите на anna@example.com", "пишите на anna@example.com"},
		{"символ &#35;1 и `@уже`", "символ &#35;1 и `@уже`"},
		{"версия 2.4.1, шаг 3", "версия 2.4.1, шаг 3"},
	} {
		if got := inert(tc.in); got != tc.want {
			t.Errorf("inert(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
