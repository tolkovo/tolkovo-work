package clamav

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeClamd accepts one INSTREAM, collects the stream and answers with
// reply(stream).
func fakeClamd(t *testing.T, reply func(stream []byte) string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				cmd, err := r.ReadString(0)
				if err != nil || cmd != "zINSTREAM\x00" {
					_, _ = conn.Write([]byte("UNKNOWN COMMAND\x00"))
					return
				}
				var stream bytes.Buffer
				for {
					var size [4]byte
					if _, err := io.ReadFull(r, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n == 0 {
						break
					}
					if _, err := io.CopyN(&stream, r, int64(n)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte(reply(stream.Bytes()) + "\x00"))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestScan(t *testing.T) {
	addr := fakeClamd(t, func(stream []byte) string {
		if bytes.Contains(stream, []byte("EICAR")) {
			return "stream: Eicar-Test-Signature FOUND"
		}
		if len(stream) > 200<<10 {
			return "INSTREAM size limit exceeded. ERROR"
		}
		return "stream: OK"
	})
	s := New(addr, 5*time.Second)
	ctx := context.Background()

	// Several frames: the clean file is larger than one chunk.
	clean := bytes.Repeat([]byte("log line\n"), 10000)
	if v, err := s.Scan(ctx, bytes.NewReader(clean)); err != nil || v.Infected {
		t.Fatalf("clean file: %+v, %v", v, err)
	}
	v, err := s.Scan(ctx, strings.NewReader("X5O!P%@AP EICAR test"))
	if err != nil || !v.Infected || v.Signature != "Eicar-Test-Signature" {
		t.Fatalf("infected file: %+v, %v", v, err)
	}
	if _, err := s.Scan(ctx, bytes.NewReader(make([]byte, 300<<10))); !errors.Is(err, ErrScan) {
		t.Fatalf("size limit: %v, want ErrScan", err)
	}
	// Nobody listens: no verdict, never "clean".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := ln.Addr().String()
	_ = ln.Close()
	if _, err := New(gone, time.Second).Scan(ctx, strings.NewReader("x")); !errors.Is(err, ErrScan) {
		t.Fatalf("unreachable: %v, want ErrScan", err)
	}
}
