package ingest

import (
	"bytes"
	"unicode/utf8"
)

// sniffLen is how much of a file the type check reads.
const sniffLen = 8 << 10

// Signatures of the binary types an app may send.
var (
	sigPNG      = []byte("\x89PNG\r\n\x1a\n")
	sigJPEG     = []byte{0xFF, 0xD8, 0xFF}
	sigZIP      = []byte("PK\x03\x04")
	sigZIPEmpty = []byte("PK\x05\x06")
	sigGzip     = []byte{0x1F, 0x8B}
	bomUTF8     = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE  = []byte{0xFF, 0xFE}
	bomUTF16BE  = []byte{0xFE, 0xFF}
)

// matchesType reports whether the first bytes of a file fit its declared
// type; full means head is a full sample, not the whole file. Binary types
// are checked by signature. Text types must look like text: UTF-8 without
// NUL bytes, or UTF-16 with a byte order mark, as Windows apps write logs;
// JSON must start with { or [, XML with <. So an executable cannot pass for
// a log, nor a page for a screenshot.
func matchesType(contentType string, head []byte, full bool) bool {
	switch contentType {
	case "image/png":
		return bytes.HasPrefix(head, sigPNG)
	case "image/jpeg":
		return bytes.HasPrefix(head, sigJPEG)
	case "application/zip":
		return bytes.HasPrefix(head, sigZIP) || bytes.HasPrefix(head, sigZIPEmpty)
	case "application/gzip":
		return bytes.HasPrefix(head, sigGzip)
	case "text/plain", "text/csv":
		return isText(head, full)
	case "application/json":
		return isText(head, full) && firstSymbol(head, "{[")
	case "application/xml", "text/xml":
		return isText(head, full) && firstSymbol(head, "<")
	}
	return false
}

func isUTF16(head []byte) bool {
	return bytes.HasPrefix(head, bomUTF16LE) || bytes.HasPrefix(head, bomUTF16BE)
}

// isText: UTF-16 with a BOM (its content is not checked), or valid UTF-8
// without NUL. A full sample may end in the middle of a rune.
func isText(head []byte, full bool) bool {
	if isUTF16(head) {
		return true
	}
	head = bytes.TrimPrefix(head, bomUTF8)
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	if full {
		for k := 1; k < utf8.UTFMax && k <= len(head); k++ {
			if utf8.RuneStart(head[len(head)-k]) {
				if !utf8.FullRune(head[len(head)-k:]) {
					head = head[:len(head)-k]
				}
				break
			}
		}
	}
	return utf8.Valid(head)
}

// firstSymbol: the first non-space character of UTF-8 text is one of set.
func firstSymbol(head []byte, set string) bool {
	if isUTF16(head) {
		return true
	}
	head = bytes.TrimLeft(bytes.TrimPrefix(head, bomUTF8), " \t\r\n")
	return len(head) > 0 && bytes.IndexByte([]byte(set), head[0]) >= 0
}
