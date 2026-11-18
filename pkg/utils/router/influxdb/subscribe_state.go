// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"
)

// The same buffer, health-check interval and retry delay as go-redis's PubSub.Channel().
const (
	subscribeBuffer        = 100
	subscribeCheckInterval = 3 * time.Second
	subscribeRetryDelay    = 100 * time.Millisecond
)

// stateSubscription is a Redis subscription read by its own loop so that it can tell whether it is live.
// go-redis's Channel() reconnects and resubscribes inside the client and never reports a lost
// connection: its channel stays open and silent while Redis is gone. This loop receives, pings and
// reconnects the same way and reports each change.
type stateSubscription struct {
	client   goRedis.UniversalClient
	channels []string
	state    func(up bool)
	out      chan *goRedis.Message

	mu     sync.Mutex
	closed bool
	pubSub *goRedis.PubSub
}

func newStateSubscription(
	ctx context.Context, client goRedis.UniversalClient, state func(up bool), channels []string,
) *stateSubscription {
	return &stateSubscription{
		client:   client,
		channels: channels,
		state:    state,
		out:      make(chan *goRedis.Message, subscribeBuffer),
		pubSub:   client.Subscribe(ctx, channels...),
	}
}

func (s *stateSubscription) current() *goRedis.PubSub {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pubSub
}

// renew drops a connection that stopped answering pings and subscribes on a new one; nil once closed.
func (s *stateSubscription) renew(ctx context.Context) *goRedis.PubSub {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	_ = s.pubSub.Close()
	s.pubSub = s.client.Subscribe(ctx, s.channels...)
	return s.pubSub
}

func (s *stateSubscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	_ = s.pubSub.Close()
}

// report passes a change on unless the subscription is being stopped: a stop is not an outage, and
// after a config reload the stopped loop must not overwrite what the new one reported.
func (s *stateSubscription) report(ctx context.Context, up bool) {
	if s.state != nil && ctx.Err() == nil {
		s.state(up)
	}
}

func (s *stateSubscription) run(ctx context.Context) {
	stop := context.AfterFunc(ctx, s.close)
	defer stop()
	defer close(s.out)
	defer s.close()

	pubSub := s.current()
	pingPending, failures := false, 0
	for {
		msg, err := pubSub.ReceiveTimeout(ctx, subscribeCheckInterval)
		if ctx.Err() != nil || errors.Is(err, goRedis.ErrClosed) {
			return
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// Nothing arrived within the check interval.
				if pingPending {
					// Not even the answer to the last ping: the connection is gone without an error.
					s.report(ctx, false)
					if pubSub = s.renew(ctx); pubSub == nil {
						return
					}
					pingPending = false
					continue
				}
				// A failed write makes go-redis reconnect and resubscribe; Redis confirms the
				// subscription again on the new connection.
				if pingErr := pubSub.Ping(ctx); pingErr != nil {
					s.report(ctx, false)
					continue
				}
				pingPending = true
				continue
			}
			// The receive failed: go-redis has dropped the connection and reconnects on the next one.
			s.report(ctx, false)
			pingPending = false
			if failures++; failures > 1 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(subscribeRetryDelay):
				}
			}
			continue
		}

		pingPending, failures = false, 0
		switch m := msg.(type) {
		case *goRedis.Subscription:
			if m.Kind == "subscribe" {
				s.report(ctx, true)
			}
		case *goRedis.Message:
			select {
			case s.out <- m:
			case <-ctx.Done():
				return
			}
		}
	}
}
