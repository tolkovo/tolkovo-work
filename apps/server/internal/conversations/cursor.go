package conversations

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cursors are opaque to clients (docs/00 §8): base64url of the sort key of
// the last item on the previous page. Lists sorted by id (UUIDv7) carry the
// id; the inbox, sorted by activity, carries last_message_at and id.

func encodeIDCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

func decodeIDCursor(c string) (*uuid.UUID, error) {
	if c == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, errBadCursor
	}
	id, err := uuid.Parse(string(raw))
	if err != nil {
		return nil, errBadCursor
	}
	return &id, nil
}

func encodeTimeCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(t.UnixMicro(), 10) + "." + id.String()))
}

func decodeTimeCursor(c string) (*time.Time, *uuid.UUID, error) {
	if c == "" {
		return nil, nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, nil, errBadCursor
	}
	ts, idPart, ok := strings.Cut(string(raw), ".")
	if !ok {
		return nil, nil, errBadCursor
	}
	micros, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, nil, errBadCursor
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return nil, nil, errBadCursor
	}
	t := time.UnixMicro(micros).UTC()
	return &t, &id, nil
}

var errBadCursor = fmt.Errorf("%w: cursor is malformed", ErrInvalid)

// paginate turns lim+1 fetched rows into a page: the extra row only tells
// that another page exists.
func paginate[R, T any](rows []R, lim int32, item func(R) T, cursor func(R) string) Page[T] {
	n := min(len(rows), int(lim))
	page := Page[T]{Items: make([]T, 0, n)}
	for _, r := range rows[:n] {
		page.Items = append(page.Items, item(r))
	}
	if len(rows) > n && n > 0 {
		page.NextCursor = cursor(rows[n-1])
	}
	return page
}
