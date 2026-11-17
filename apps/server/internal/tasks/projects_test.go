package tasks

import "testing"

// Refs of tasks (ADR-0022): KEY-N of a project, #N or N of the default one.
func TestParseRef(t *testing.T) {
	for in, want := range map[string]struct {
		key string
		n   int64
		ok  bool
	}{
		"CRM-41":               {"CRM", 41, true},
		"crm-41":               {"CRM", 41, true},
		" TSK-1 ":              {"TSK", 1, true},
		"#42":                  {"", 42, true},
		"42":                   {"", 42, true},
		"0":                    {},
		"CRM-0":                {},
		"C-1":                  {},
		"TOOLONG-1":            {},
		"CRM 41":               {},
		"CRM-":                 {},
		"":                     {},
		"99999999999999999999": {},
	} {
		key, n, ok := ParseRef(in)
		if key != want.key || n != want.n || ok != want.ok {
			t.Errorf("ParseRef(%q) = %q %d %v, want %+v", in, key, n, ok, want)
		}
	}
	if got := Ref("CRM", 41); got != "CRM-41" {
		t.Errorf("Ref: %s", got)
	}
}
