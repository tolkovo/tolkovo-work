package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// UpdateState is the position of a personal account in its update stream
// (MTProto pts, qts, date, seq).
type UpdateState struct {
	Pts, Qts, Date, Seq int
}

// StateStore keeps the update state of one account between connections.
type StateStore interface {
	// LoadState returns found = false before the first connection: then
	// the stream starts now, without history.
	LoadState(ctx context.Context) (state UpdateState, found bool, err error)
	SaveState(ctx context.Context, state UpdateState) error
}

// Fetch downloads a file of a received message through the connection that
// received it, at most limit bytes.
type Fetch func(ctx context.Context, f File, w io.Writer, limit int64) error

// ListenOptions configure Listen.
type ListenOptions struct {
	Session []byte
	// SaveSession stores the session when Telegram changes it (a new
	// data center, keys).
	SaveSession func(ctx context.Context, sess []byte) error
	State       StateStore
	// Handle gets the private messages of people, one at a time and in
	// order. An error is logged by gotd and the message is not delivered
	// again, so Handle retries what it can by itself.
	Handle func(ctx context.Context, m Message, fetch Fetch) error
	// OnReady is called once the account is connected and receiving.
	OnReady func()
}

// Listen keeps a personal account online and passes its new private
// messages to Handle until ctx is done (ADR-0014). Missed updates since the
// saved state are fetched first (updates.getDifference). It returns
// ErrSessionRevoked when the session was ended in Telegram, ErrBanned for
// a banned account, and ctx.Err() on a normal stop.
func (u *UserAuth) Listen(ctx context.Context, opts ListenOptions) error {
	sess := &persistentSession{data: opts.Session, save: opts.SaveSession}
	l := &listener{handle: opts.Handle, people: map[int64]Person{}, bots: map[int64]bool{}}
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(l.onNewMessage)
	gaps := updates.New(updates.Config{
		Handler: dispatcher,
		Storage: &stateAdapter{store: opts.State},
		// Channels are never read; their gaps are not worth a burst.
		MaxChannelDifferenceConcurrency: 1,
	})
	client := gotd.NewClient(u.appID, u.appHash, u.options(gotd.Options{
		SessionStorage: sess,
		Device:         u.device,
		UpdateHandler:  gaps,
	}))
	l.fetch = func(ctx context.Context, f File, w io.Writer, limit int64) error {
		if f.location == nil {
			return errors.New("telegram: not a file of this account")
		}
		if f.Size > limit {
			return ErrFileTooLarge
		}
		_, err := downloader.NewDownloader().Download(client.API(), f.location).Stream(ctx, &limitWriter{w: w, n: limit})
		return classify(err)
	}
	err := client.Run(ctx, func(ctx context.Context) error {
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		l.selfID = self.ID
		return gaps.Run(ctx, client.API(), self.ID, updates.AuthOptions{
			OnStart: func(context.Context) {
				if opts.OnReady != nil {
					opts.OnReady()
				}
			},
		})
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return classify(err)
}

type listener struct {
	selfID int64
	handle func(ctx context.Context, m Message, fetch Fetch) error
	fetch  Fetch
	// people and bots remember users from earlier updates: a short update
	// for a known user comes without the user.
	mu     sync.Mutex
	people map[int64]Person
	bots   map[int64]bool
}

func (l *listener) onNewMessage(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
	l.remember(e.Users)
	m, ok := u.Message.(*tg.Message)
	if !ok {
		return nil // a service message or an empty one
	}
	msg, ok := l.convert(m)
	if !ok {
		return nil
	}
	return l.handle(ctx, msg, l.fetch)
}

func (l *listener) remember(users map[int64]*tg.User) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, u := range users {
		if u.Min {
			continue // may lack fields
		}
		l.people[id] = Person{ID: id, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username}
		l.bots[id] = u.Bot
	}
}

// convert keeps private chats with people: not groups or channels, not
// Saved Messages, not the service account, not bots.
func (l *listener) convert(m *tg.Message) (Message, bool) {
	peer, ok := m.PeerID.(*tg.PeerUser)
	if !ok || peer.UserID == l.selfID || peer.UserID == ServiceUserID {
		return Message{}, false
	}
	l.mu.Lock()
	person, known := l.people[peer.UserID]
	bot := l.bots[peer.UserID]
	l.mu.Unlock()
	if bot {
		return Message{}, false
	}
	if !known {
		person = Person{ID: peer.UserID}
	}
	msg := Message{ID: int64(m.ID), ChatID: peer.UserID, From: person, Out: m.Out, Text: m.Message}
	msg.Files, msg.Unsupported = mediaOf(m.Media)
	if msg.Text == "" && len(msg.Files) == 0 && msg.Unsupported == "" {
		return Message{}, false
	}
	if msg.Text != "" || len(msg.Files) > 0 {
		msg.Unsupported = ""
	}
	return msg, true
}

// mediaOf returns the file of a message or what it holds instead.
func mediaOf(media tg.MessageMediaClass) ([]File, string) {
	switch md := media.(type) {
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return nil, ""
	case *tg.MessageMediaPhoto:
		photo, ok := md.Photo.(*tg.Photo)
		if !ok {
			return nil, ""
		}
		size, thumb := largestPhoto(photo.Sizes)
		if thumb == "" {
			return nil, ""
		}
		return []File{{
			Name: "photo.jpg", MIME: "image/jpeg", Size: size,
			location: &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: thumb},
		}}, ""
	case *tg.MessageMediaDocument:
		doc, ok := md.Document.(*tg.Document)
		if !ok {
			return nil, ""
		}
		name, unsupported := documentName(doc)
		if unsupported != "" {
			return nil, unsupported
		}
		mime := doc.MimeType
		if mime == "" {
			mime = "application/octet-stream"
		}
		return []File{{
			Name: name, MIME: mime, Size: doc.Size,
			location: &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference},
		}}, ""
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return nil, UnsupportedLocation
	case *tg.MessageMediaContact:
		return nil, UnsupportedContact
	case *tg.MessageMediaPoll:
		return nil, UnsupportedPoll
	default:
		return nil, UnsupportedOther
	}
}

// largestPhoto picks the biggest downloadable size of a photo.
func largestPhoto(sizes []tg.PhotoSizeClass) (int64, string) {
	var (
		best  int64
		thumb string
	)
	for _, s := range sizes {
		var n int64
		var typ string
		switch v := s.(type) {
		case *tg.PhotoSize:
			n, typ = int64(v.Size), v.Type
		case *tg.PhotoSizeProgressive:
			if len(v.Sizes) > 0 {
				n, typ = int64(v.Sizes[len(v.Sizes)-1]), v.Type
			}
		default:
			continue // stripped and cached previews are not files
		}
		if typ != "" && n >= best {
			best, thumb = n, typ
		}
	}
	return best, thumb
}

// documentName names a document by its attributes; stickers are not files
// for the inbox.
func documentName(doc *tg.Document) (string, string) {
	name := "file"
	for _, a := range doc.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeSticker, *tg.DocumentAttributeCustomEmoji:
			return "", UnsupportedSticker
		case *tg.DocumentAttributeFilename:
			if v.FileName != "" {
				return v.FileName, ""
			}
		case *tg.DocumentAttributeAudio:
			if v.Voice {
				name = "voice.ogg"
			} else {
				name = "audio.mp3"
			}
		case *tg.DocumentAttributeVideo:
			name = "video.mp4"
		}
	}
	return name, ""
}

// persistentSession is the session storage of a listening connection: it
// starts from the stored session and saves every change.
type persistentSession struct {
	mu   sync.Mutex
	data []byte
	save func(ctx context.Context, sess []byte) error
}

func (s *persistentSession) LoadSession(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return nil, errors.New("telegram: no session")
	}
	return append([]byte(nil), s.data...), nil
}

func (s *persistentSession) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	s.data = append([]byte(nil), data...)
	s.mu.Unlock()
	if s.save == nil {
		return nil
	}
	if err := s.save(ctx, data); err != nil {
		return fmt.Errorf("telegram: save session: %w", err)
	}
	return nil
}

// stateAdapter stores gotd's update state through a StateStore. Channel
// positions stay in memory: channels are not read, and a restart simply
// forgets them.
type stateAdapter struct {
	store StateStore
	mu    sync.Mutex
	cur   *UpdateState
	chans map[int64]int
}

var errNoState = errors.New("telegram: no update state yet")

func (a *stateAdapter) GetState(ctx context.Context, _ int64) (updates.State, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, found, err := a.store.LoadState(ctx)
	if err != nil || !found {
		return updates.State{}, false, err
	}
	a.cur = &st
	return updates.State(st), true, nil
}

func (a *stateAdapter) SetState(ctx context.Context, _ int64, s updates.State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := UpdateState(s)
	if err := a.store.SaveState(ctx, st); err != nil {
		return err
	}
	a.cur = &st
	return nil
}

func (a *stateAdapter) change(ctx context.Context, f func(*UpdateState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil {
		return errNoState
	}
	next := *a.cur
	f(&next)
	if err := a.store.SaveState(ctx, next); err != nil {
		return err
	}
	a.cur = &next
	return nil
}

func (a *stateAdapter) SetPts(ctx context.Context, _ int64, pts int) error {
	return a.change(ctx, func(s *UpdateState) { s.Pts = pts })
}

func (a *stateAdapter) SetQts(ctx context.Context, _ int64, qts int) error {
	return a.change(ctx, func(s *UpdateState) { s.Qts = qts })
}

func (a *stateAdapter) SetDate(ctx context.Context, _ int64, date int) error {
	return a.change(ctx, func(s *UpdateState) { s.Date = date })
}

func (a *stateAdapter) SetSeq(ctx context.Context, _ int64, seq int) error {
	return a.change(ctx, func(s *UpdateState) { s.Seq = seq })
}

func (a *stateAdapter) SetDateSeq(ctx context.Context, _ int64, date, seq int) error {
	return a.change(ctx, func(s *UpdateState) { s.Date, s.Seq = date, seq })
}

func (a *stateAdapter) GetChannelPts(_ context.Context, _, channelID int64) (int, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pts, ok := a.chans[channelID]
	return pts, ok, nil
}

func (a *stateAdapter) SetChannelPts(_ context.Context, _, channelID int64, pts int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chans == nil {
		a.chans = map[int64]int{}
	}
	a.chans[channelID] = pts
	return nil
}

func (a *stateAdapter) ForEachChannels(ctx context.Context, _ int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	a.mu.Lock()
	chans := make(map[int64]int, len(a.chans))
	for k, v := range a.chans {
		chans[k] = v
	}
	a.mu.Unlock()
	for id, pts := range chans {
		if err := f(ctx, id, pts); err != nil {
			return err
		}
	}
	return nil
}
