package ingest

import "testing"

func TestFileType(t *testing.T) {
	for _, c := range []struct{ name, declared, want string }{
		{"app.log", "text/plain; charset=utf-8", "text/plain"},
		{"app.log", "application/octet-stream", "text/plain"},
		{"Main.LOG", "text/x-log", "text/plain"},
		{"notes.txt", "application/octet-stream", "text/plain"},
		{"dump.bin", "application/octet-stream", "application/octet-stream"},
		{"screen.png", "image/png", "image/png"},
		{"run.log.exe", "application/octet-stream", "application/octet-stream"},
	} {
		got, err := fileType(c.name, c.declared)
		if err != nil || got != c.want {
			t.Errorf("fileType(%s, %s) = %q, %v; want %q", c.name, c.declared, got, err, c.want)
		}
	}
	if _, err := fileType("app.log", "not a type"); err == nil {
		t.Error("a broken type is accepted")
	}
}
