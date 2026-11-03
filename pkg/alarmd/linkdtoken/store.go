// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package linkdtoken keeps the token alarmd calls the link's API with when
// the admin serves it: the admin issues the token and hands it over on the
// front surface, every process of the deployment reads it from the runtime
// store, and a process the admin refuses with it says so, for the admin to
// hand over another. alarmd never refreshes it itself: the admin that would
// refresh it is the server of the link's API, so when it cannot hand over a
// new one the link cannot be read either.
package linkdtoken

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// The states of the token, closed. valid: held and usable; refresh_due:
// usable, and past the time the admin's token says it should be replaced;
// none: never handed over, or gone with its life; expired: past its expiry;
// rejected: the admin answered 401 to a call made with exactly this token.
// Every state but valid asks the admin for a new token.
const (
	StateValid      = "valid"
	StateRefreshDue = "refresh_due"
	StateNone       = "none"
	StateExpired    = "expired"
	StateRejected   = "rejected"
)

// States is every state, in the order a reader shows them.
var States = []string{StateValid, StateRefreshDue, StateNone, StateExpired, StateRejected}

// Answer is the token's state as the front surface answers it. ExpiresAt is
// absent when no token is held. The token itself is never in it.
type Answer struct {
	State     string `json:"state"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ErrInvalidToken is a token that is not one the admin issues, or one past
// its expiry when handed over.
var ErrInvalidToken = errors.New("alarmd linkdtoken: not a usable admin token")

// ErrNoToken is no usable token held: none handed over, expired, or refused
// by the admin. The call that needed it is not made.
type ErrNoToken struct{ State string }

func (err ErrNoToken) Error() string {
	return "alarmd linkdtoken: no usable token for the link's API (" + err.State + "); the admin hands one over"
}

// reread is how long a process uses the token it last read before reading
// the store again: a token handed over to another process is used here
// within it, and a refusal re-reads at once.
const reread = 30 * time.Second

// tokenShape is the admin's token: three base64url parts, at most 4096
// bytes, as its grant schema allows.
var tokenShape = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// record is the token as kept, with the two times read from its claims and
// whether the admin refused it.
type record struct {
	Token    string `json:"token"`
	Exp      int64  `json:"exp"`
	Rfa      int64  `json:"rfa"`
	Rejected bool   `json:"rejected,omitempty"`
}

// Store is the deployment's token in the runtime store, one key under the
// deployment's prefix, living as long as the token does.
type Store struct {
	client redis.UniversalClient
	key    string
	now    func() time.Time

	mu       sync.Mutex
	cached   record
	cachedAt time.Time
	read     bool
}

// New is the store of the token under prefix; now nil is the wall clock.
func New(client redis.UniversalClient, prefix string, now func() time.Time) (*Store, error) {
	if client == nil || prefix == "" {
		return nil, errors.New("alarmd linkdtoken: a store needs a client and a prefix")
	}
	if now == nil {
		now = time.Now
	}
	return &Store{client: client, key: prefix + ":linkd-token", now: now}, nil
}

// claims reads the expiry and refresh time out of a token's claims. The
// signature is the admin's to check, with a key alarmd does not hold; what is
// read here only decides when to ask for another.
func claims(token string) (exp, rfa int64, err error) {
	if len(token) > 4096 || !tokenShape.MatchString(token) {
		return 0, 0, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		return 0, 0, ErrInvalidToken
	}
	var c struct {
		Iat *int64 `json:"iat"`
		Exp *int64 `json:"exp"`
		Rfa *int64 `json:"rfa"`
	}
	if json.Unmarshal(payload, &c) != nil || c.Iat == nil || c.Exp == nil || c.Rfa == nil ||
		*c.Rfa <= *c.Iat || *c.Exp <= *c.Rfa {
		return 0, 0, ErrInvalidToken
	}
	return *c.Exp, *c.Rfa, nil
}

// Put keeps token as the deployment's, replacing any other and clearing a
// refusal, and answers its state.
func (s *Store) Put(ctx context.Context, token string) (Answer, error) {
	exp, rfa, err := claims(token)
	if err != nil {
		return Answer{}, err
	}
	now := s.now()
	life := time.Unix(exp, 0).Sub(now)
	if life <= 0 {
		return Answer{}, ErrInvalidToken
	}
	kept := record{Token: token, Exp: exp, Rfa: rfa}
	payload, err := json.Marshal(kept)
	if err != nil {
		return Answer{}, err
	}
	if err := s.client.Set(ctx, s.key, payload, life).Err(); err != nil {
		return Answer{}, fmt.Errorf("alarmd linkdtoken: keep token: %w", err)
	}
	s.mu.Lock()
	s.cached, s.cachedAt, s.read = kept, now, true
	s.mu.Unlock()
	return answerOf(kept, true, now), nil
}

// State reads the token's state from the store.
func (s *Store) State(ctx context.Context) (Answer, error) {
	kept, found, err := s.load(ctx)
	if err != nil {
		return Answer{}, err
	}
	return answerOf(kept, found, s.now()), nil
}

// Token is the token to call the link's API with: the one this process last
// read, read again from the store once it is reread old. A token the admin
// refused, or past its expiry, is no token; one past its refresh time is
// still used until the admin hands over the next.
//
// A store that does not answer fails the call rather than falling back to
// the token read before: that one may have been refused by another process
// meanwhile, and the store is the runtime store this process cannot run
// without anyway. A failed read does not count as a read, so while the store
// is down every call asks it again - deliberately unbounded: the client holds
// two connections, and each call that asks fails at once instead of calling
// the link.
func (s *Store) Token(ctx context.Context) (string, error) {
	now := s.now()
	s.mu.Lock()
	kept, fresh := s.cached, s.read && now.Sub(s.cachedAt) < reread
	s.mu.Unlock()
	found := kept.Token != ""
	if !fresh {
		var err error
		if kept, found, err = s.load(ctx); err != nil {
			return "", err
		}
		s.mu.Lock()
		s.cached, s.cachedAt, s.read = kept, now, true
		s.mu.Unlock()
	}
	switch state := answerOf(kept, found, now).State; state {
	case StateValid, StateRefreshDue:
		return kept.Token, nil
	default:
		return "", ErrNoToken{State: state}
	}
}

// rejectScript marks the kept token refused when it is still the one the
// refused call was made with, keeping its life: a token handed over since
// is not touched.
var rejectScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local kept = cjson.decode(raw)
if kept['token'] ~= ARGV[1] then return 0 end
local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 then return 0 end
kept['rejected'] = true
redis.call('SET', KEYS[1], cjson.encode(kept), 'PX', ttl)
return 1
`)

// Rejected records that the admin answered 401 to a call made with token.
// The next Token reads the store again, so a token handed over meanwhile is
// used at once.
func (s *Store) Rejected(ctx context.Context, token string) error {
	s.mu.Lock()
	s.read = false
	s.mu.Unlock()
	if token == "" {
		return nil
	}
	if err := rejectScript.Run(ctx, s.client, []string{s.key}, token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("alarmd linkdtoken: record refusal: %w", err)
	}
	return nil
}

func (s *Store) load(ctx context.Context) (record, bool, error) {
	raw, err := s.client.Get(ctx, s.key).Bytes()
	if errors.Is(err, redis.Nil) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, fmt.Errorf("alarmd linkdtoken: read token: %w", err)
	}
	var kept record
	if json.Unmarshal(raw, &kept) != nil || kept.Token == "" {
		// Not a record this process wrote: no token, for the admin to
		// replace, rather than a failure every call repeats.
		return record{}, false, nil
	}
	return kept, true, nil
}

func answerOf(kept record, found bool, now time.Time) Answer {
	if !found {
		return Answer{State: StateNone}
	}
	answer := Answer{ExpiresAt: time.Unix(kept.Exp, 0).UTC().Format(time.RFC3339)}
	switch {
	case now.Unix() >= kept.Exp:
		answer.State = StateExpired
	case kept.Rejected:
		answer.State = StateRejected
	case now.Unix() >= kept.Rfa:
		answer.State = StateRefreshDue
	default:
		answer.State = StateValid
	}
	return answer
}
