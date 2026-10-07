package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
)

// Error codes of a channel the worker stopped receiving for; the admin
// reconnects it (docs/02 §11.5, ADR-0014).
const (
	ErrorTokenRevoked   = "token_revoked"
	ErrorSessionRevoked = "session_revoked"
	ErrorBanned         = "banned"
)

// Lease timing: one worker holds a channel's connection at a time
// (ADR-0014). A worker that cannot renew drops the connection before the
// lease runs out.
const (
	leaseTTL   = 30 * time.Second
	leaseRenew = 10 * time.Second
)

// ReceiverOptions configure RunReceivers.
type ReceiverOptions struct {
	// System lists the channels of every workspace (tolkovo_system, policy
	// system_see_active).
	System store.DBTX
	Redis  *redis.Client
	// Refresh is how often the list of channels is read; 15 s by default.
	Refresh time.Duration
	// PollTimeout is the long-poll wait of getUpdates; 10 s by default.
	PollTimeout time.Duration
}

// RunReceivers holds the connections of the channels Telegram does not
// push to: personal accounts (MTProto) and bots without a webhook
// (getUpdates). It runs in the worker role until ctx is done. With several
// workers, a Redis lease per channel keeps one connection per channel.
func (s *Service) RunReceivers(ctx context.Context, opts ReceiverOptions) error {
	if opts.Refresh <= 0 {
		opts.Refresh = 15 * time.Second
	}
	if opts.PollTimeout <= 0 {
		opts.PollTimeout = 10 * time.Second
	}
	node, err := randomHex(8)
	if err != nil {
		return err
	}
	r := &receivers{svc: s, opts: opts, node: node, running: map[uuid.UUID]context.CancelFunc{}}
	defer r.stopAll()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		s.runActions(ctx)
	}()
	tick := time.NewTicker(opts.Refresh)
	defer tick.Stop()
	for {
		r.refresh(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

type receivers struct {
	svc  *Service
	opts ReceiverOptions
	node string

	mu      sync.Mutex
	running map[uuid.UUID]context.CancelFunc
	wg      sync.WaitGroup
}

// refresh starts the channels nobody here runs yet and stops the ones that
// are no longer active.
func (r *receivers) refresh(ctx context.Context) {
	if r.svc.opts.Sealer == nil {
		return
	}
	rows, err := store.New(r.opts.System).ListReceivers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.svc.log.Warn("list channels to receive", "error", err)
		}
		return
	}
	wanted := make(map[uuid.UUID]bool, len(rows))
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		if row.Type == TypeTelegramUser && r.svc.opts.Users == nil {
			continue
		}
		wanted[row.ID] = true
		if _, ok := r.running[row.ID]; ok {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		r.running[row.ID] = cancel
		r.wg.Add(1)
		go func(ws, id uuid.UUID, typ string) {
			defer r.wg.Done()
			r.run(cctx, ws, id, typ)
			r.mu.Lock()
			delete(r.running, id)
			r.mu.Unlock()
			cancel()
		}(row.WorkspaceID, row.ID, row.Type)
	}
	for id, cancel := range r.running {
		if !wanted[id] {
			cancel()
		}
	}
}

func (r *receivers) stopAll() {
	r.mu.Lock()
	for _, cancel := range r.running {
		cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// run holds one channel while this worker has its lease. It returns when
// the lease is elsewhere, the connection ends or ctx is done; refresh
// starts it again on the next round.
func (r *receivers) run(ctx context.Context, workspaceID, channelID uuid.UUID, typ string) {
	l := &lease{rdb: r.opts.Redis, key: "lease:channel:" + channelID.String(), token: r.node}
	ok, err := l.acquire(ctx)
	if err != nil || !ok {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		l.release(context.WithoutCancel(ctx))
	}()
	go l.keep(ctx, cancel)

	switch typ {
	case TypeTelegramBot:
		err = r.svc.pollBot(ctx, workspaceID, channelID, r.opts.PollTimeout)
	case TypeTelegramUser:
		err = r.svc.listenUser(ctx, workspaceID, channelID)
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	code := ""
	switch {
	case errors.Is(err, ErrBotToken):
		code = ErrorTokenRevoked
	case errors.Is(err, telegram.ErrSessionRevoked):
		code = ErrorSessionRevoked
	case errors.Is(err, telegram.ErrBanned):
		code = ErrorBanned
	}
	if code == "" {
		r.svc.log.Warn("telegram receiver stopped, restarting", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return
	}
	r.svc.log.Warn("telegram channel needs the admin", "workspace_id", workspaceID, "channel_id", channelID, "error_code", code)
	r.svc.setError(context.WithoutCancel(ctx), workspaceID, channelID, code)
}

// pollBot long-polls a bot's updates. The offset lives in memory: Telegram
// keeps unconfirmed updates for a day, and a repeat after a restart is
// deduplicated by message id.
func (s *Service) pollBot(ctx context.Context, workspaceID, channelID uuid.UUID, timeout time.Duration) error {
	ch, raw, err := s.loadActive(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	if ch.Details.Delivery != DeliveryPolling {
		return nil
	}
	var sec botSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return fmt.Errorf("decode bot secret: %w", err)
	}
	fetch := s.botFetch(sec.Token)
	var offset int64
	for ctx.Err() == nil {
		ups, err := s.opts.Bots.GetUpdates(ctx, sec.Token, offset, int(timeout/time.Second))
		var apiErr *telegram.APIError
		switch {
		case errors.Is(err, telegram.ErrBotToken):
			return ErrBotToken
		case errors.As(err, &apiErr) && apiErr.RetryAfter > 0:
			if sleep(ctx, apiErr.RetryAfter) != nil {
				return nil //nolint:nilerr // stopping, not failing
			}
			continue
		case err != nil:
			return err
		}
		for _, u := range ups {
			if u.Message != nil {
				if err := s.deliverRetrying(ctx, workspaceID, channelID, *u.Message, fetch, BotFileLimit, sec.Token); err != nil {
					return err
				}
			}
			if u.Callback != nil {
				s.chooseTopic(ctx, workspaceID, channelID, sec.Token, *u.Callback)
			}
			offset = u.ID + 1
		}
	}
	return nil
}

// listenUser keeps a personal account online (ADR-0014).
func (s *Service) listenUser(ctx context.Context, workspaceID, channelID uuid.UUID) error {
	ch, raw, err := s.loadActive(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	if ch.Type != TypeTelegramUser || s.opts.Users == nil {
		return nil
	}
	var sec userSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return fmt.Errorf("decode account secret: %w", err)
	}
	defer s.setLive(channelID, nil)
	return s.opts.Users.Listen(ctx, telegram.ListenOptions{
		Session: sec.Session,
		SaveSession: func(ctx context.Context, sess []byte) error {
			return s.saveSession(ctx, workspaceID, channelID, sess)
		},
		State: &updateState{svc: s, workspaceID: workspaceID, channelID: channelID},
		Handle: func(ctx context.Context, m telegram.Message, fetch telegram.Fetch) error {
			if s.isOutgoingEcho(ctx, channelID, m) {
				return nil
			}
			return s.deliverRetrying(ctx, workspaceID, channelID, m, fetchFunc(fetch), UserFileLimit, "")
		},
		Read: func(ctx context.Context, chatID, maxID int64) error {
			return s.opts.Inbox.MarkContactRead(ctx, workspaceID, channelID, threadOf(chatID), maxID)
		},
		SavePeer: func(ctx context.Context, p telegram.Peer) error {
			return s.savePeer(ctx, workspaceID, channelID, p)
		},
		OnReady: func(c telegram.Conn) { s.setLive(channelID, c) },
	})
}

// saveSession stores a changed session of an active account.
func (s *Service) saveSession(ctx context.Context, workspaceID, channelID uuid.UUID, sess []byte) error {
	sealed, err := s.seal(ctx, workspaceID, channelID, userSecret{Session: sess})
	if err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.GetActiveChannel(ctx, store.GetActiveChannelParams{WorkspaceID: workspaceID, ID: channelID}); err != nil {
			return err // disconnected meanwhile: do not bring the secret back
		}
		return q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: channelID, Sealed: sealed, UpdatedAt: s.now()})
	})
}

// setError marks an active channel as needing the admin.
func (s *Service) setError(ctx context.Context, workspaceID, channelID uuid.UUID, code string) {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).SetChannelError(ctx, store.SetChannelErrorParams{
			WorkspaceID: workspaceID, ID: channelID, ErrorCode: &code, UpdatedAt: s.now(),
		})
	})
	if err != nil {
		s.log.Warn("set channel error", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
	}
}

// updateState keeps a personal account's update state in Postgres.
type updateState struct {
	svc                    *Service
	workspaceID, channelID uuid.UUID
}

func (u *updateState) LoadState(ctx context.Context) (telegram.UpdateState, bool, error) {
	var (
		st    telegram.UpdateState
		found bool
	)
	err := u.svc.db.InTenantTx(ctx, u.workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetUpdateState(ctx, store.GetUpdateStateParams{WorkspaceID: u.workspaceID, ChannelID: u.channelID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		st, found = telegram.UpdateState{Pts: int(row.Pts), Qts: int(row.Qts), Date: int(row.Date), Seq: int(row.Seq)}, true
		return nil
	})
	return st, found, err
}

func (u *updateState) SaveState(ctx context.Context, st telegram.UpdateState) error {
	return u.svc.db.InTenantTx(ctx, u.workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).PutUpdateState(ctx, store.PutUpdateStateParams{
			WorkspaceID: u.workspaceID, ChannelID: u.channelID,
			Pts: int32(st.Pts), Qts: int32(st.Qts), Date: int32(st.Date), Seq: int32(st.Seq), //nolint:gosec // G115: MTProto counters are int32
		})
	})
}

// lease is a Redis lock with a TTL, renewed while its holder works.
type lease struct {
	rdb   *redis.Client
	key   string
	token string
}

var (
	renewScript = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) end return 0`)
	freeScript  = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) end return 0`)
)

func (l *lease) acquire(ctx context.Context) (bool, error) {
	return l.rdb.SetNX(ctx, l.key, l.token, leaseTTL).Result()
}

// keep renews the lease and calls lost once it cannot.
func (l *lease) keep(ctx context.Context, lost context.CancelFunc) {
	t := time.NewTicker(leaseRenew)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.token, leaseTTL.Milliseconds()).Int()
		if (err != nil || n == 0) && ctx.Err() == nil {
			lost()
			return
		}
	}
}

func (l *lease) release(ctx context.Context) {
	_ = freeScript.Run(ctx, l.rdb, []string{l.key}, l.token).Err()
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
