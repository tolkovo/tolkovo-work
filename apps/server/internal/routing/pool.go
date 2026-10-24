package routing

import (
	"slices"

	"github.com/google/uuid"
)

// unlimited is the capacity a member without a limit counts with when the
// router compares loads (least_loaded): the highest limit one may set.
const unlimited = MaxChatsLimit

// pool is who one dispatch run gives conversations to (ADR-0021): the
// available members in turn order, with their load and capacity. It
// implements conversations.Pool.
type pool struct {
	turn  []uuid.UUID       // turn order: whoever got the last one is at the end
	load  map[uuid.UUID]int // conversations in work
	limit map[uuid.UUID]int // capacity; 0 is no limit
	least bool              // least_loaded rather than round_robin
}

func (p *pool) room(u uuid.UUID) bool { return p.limit[u] == 0 || p.load[u] < p.limit[u] }

// share is how full a member is, for least_loaded.
func (p *pool) share(u uuid.UUID) float64 {
	c := p.limit[u]
	if c == 0 {
		c = unlimited
	}
	return float64(p.load[u]) / float64(c)
}

// Users implements conversations.Pool.
func (p *pool) Users() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(p.turn))
	for _, u := range p.turn {
		if p.room(u) {
			out = append(out, u)
		}
	}
	return out
}

// Pick implements conversations.Pool: the first in turn with room for whom
// may holds, or with least_loaded the least full of them, the first in
// turn among equals. The chosen one goes to the end of the turn.
func (p *pool) Pick(may func(uuid.UUID) bool) (uuid.UUID, bool) {
	best := -1
	for i, u := range p.turn {
		if !p.room(u) || !may(u) {
			continue
		}
		if best < 0 {
			best = i
			if !p.least {
				break
			}
			continue
		}
		if p.share(u) < p.share(p.turn[best]) {
			best = i
		}
	}
	if best < 0 {
		return uuid.Nil, false
	}
	u := p.turn[best]
	p.turn = append(slices.Delete(p.turn, best, best+1), u)
	p.load[u]++
	return u, true
}
