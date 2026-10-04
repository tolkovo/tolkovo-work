package ingest

import (
	"bytes"
	"testing"
)

func TestMatchesType(t *testing.T) {
	// "журнал" cut after the first byte of "л": a full sample may end mid-rune.
	cut := append(bytes.Repeat([]byte("a"), sniffLen-3), "жл"[:3]...)
	for _, c := range []struct {
		name, ct string
		head     []byte
		full     bool
		want     bool
	}{
		{"png", "image/png", []byte("\x89PNG\r\n\x1a\n...."), false, true},
		{"page as png", "image/png", []byte("<html>"), false, false},
		{"jpeg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, false, true},
		{"png as jpeg", "image/jpeg", []byte("\x89PNG\r\n\x1a\n"), false, false},
		{"zip", "application/zip", []byte("PK\x03\x04rest"), false, true},
		{"empty zip", "application/zip", []byte("PK\x05\x06"), false, true},
		{"exe as zip", "application/zip", []byte("MZ\x90\x00"), false, false},
		{"gzip", "application/gzip", []byte{0x1F, 0x8B, 0x08}, false, true},
		{"log", "text/plain", []byte("2026-10-05 ERROR журнал\n"), false, true},
		{"utf-8 bom", "text/plain", []byte("\xEF\xBB\xBFline"), false, true},
		{"utf-16 log", "text/plain", []byte("\xFF\xFEl\x00o\x00g\x00"), false, true},
		{"exe as log", "text/plain", []byte("MZ\x90\x00\x03\x00"), false, false},
		{"latin-1 log", "text/plain", []byte("caf\xE9"), false, false},
		{"cut rune in a full sample", "text/plain", cut, true, true},
		{"cut rune at the real end", "text/plain", cut, false, false},
		{"csv", "text/csv", []byte("a,b\n1,2\n"), false, true},
		{"json", "application/json", []byte("  {\"level\":\"error\"}"), false, true},
		{"json lines", "application/json", []byte("[1,2]"), false, true},
		{"text as json", "application/json", []byte("error"), false, false},
		{"xml", "application/xml", []byte("\xEF\xBB\xBF<?xml version=\"1.0\"?>"), false, true},
		{"text as xml", "text/xml", []byte("plain"), false, false},
		{"empty json", "application/json", nil, false, false},
		{"unknown type", "application/pdf", []byte("%PDF"), false, false},
	} {
		if got := matchesType(c.ct, c.head, c.full); got != c.want {
			t.Errorf("%s: matchesType(%s) = %v, want %v", c.name, c.ct, got, c.want)
		}
	}
}
