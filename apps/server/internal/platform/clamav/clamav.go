// Package clamav scans files with clamd over TCP (INSTREAM command), the
// optional antivirus of docs/adr/0009-file-storage.md. It has no
// dependencies: the protocol is a few framed writes and one reply line.
//
// clamd refuses streams longer than its StreamMaxLength (25 MB by default);
// set it above the largest file the server accepts.
package clamav

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// chunkSize is how much is sent per INSTREAM frame.
const chunkSize = 64 << 10

// ErrScan means clamd did not give a verdict (unreachable, a size limit, an
// error reply). The file is neither clean nor infected.
var ErrScan = errors.New("clamav: scan failed")

// Scanner talks to one clamd.
type Scanner struct {
	addr    string
	timeout time.Duration
	dialer  net.Dialer
}

// New returns a scanner for clamd at addr (host:port). timeout bounds one
// whole scan; zero means two minutes.
func New(addr string, timeout time.Duration) *Scanner {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Scanner{addr: addr, timeout: timeout}
}

// Verdict is the result of a scan. Signature names what was found.
type Verdict struct {
	Infected  bool
	Signature string
}

// Scan streams r to clamd and returns its verdict.
func (s *Scanner) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, err := s.dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// Closing the connection unblocks reads and writes when ctx ends.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	w := bufio.NewWriterSize(conn, chunkSize+4)
	if _, err := w.WriteString("zINSTREAM\x00"); err != nil {
		return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
	}
	buf := make([]byte, chunkSize)
	var size [4]byte
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(size[:], uint32(n)) //nolint:gosec // G115: n ≤ chunkSize
			if _, err := w.Write(size[:]); err != nil {
				return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return Verdict{}, fmt.Errorf("clamav: read file: %w", rerr)
		}
	}
	if _, err := w.Write([]byte{0, 0, 0, 0}); err != nil {
		return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
	}
	if err := w.Flush(); err != nil {
		return Verdict{}, fmt.Errorf("%w: %w", ErrScan, err)
	}
	reply, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadBytes(0)
	if err != nil && (!errors.Is(err, io.EOF) || len(reply) == 0) {
		return Verdict{}, fmt.Errorf("%w: read reply: %w", ErrScan, err)
	}
	return parseReply(string(bytes.TrimRight(reply, "\x00\n")))
}

// parseReply reads "stream: OK", "stream: <signature> FOUND" or an error.
func parseReply(line string) (Verdict, error) {
	rest, ok := strings.CutPrefix(line, "stream: ")
	switch {
	case ok && rest == "OK":
		return Verdict{}, nil
	case ok && strings.HasSuffix(rest, " FOUND"):
		return Verdict{Infected: true, Signature: strings.TrimSuffix(rest, " FOUND")}, nil
	}
	return Verdict{}, fmt.Errorf("%w: clamd answered %q", ErrScan, line)
}
