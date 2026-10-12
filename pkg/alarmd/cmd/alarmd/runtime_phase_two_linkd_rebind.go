// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The background discovery's pacing: the first retry soon after startup, then
// doubling to a ceiling a Console outage of any length is asked at.
const (
	linkdRetryFirst   = 5 * time.Second
	linkdRetryCeiling = time.Minute
)

// linkdBinding is one place the open alert sets are read from: a Redis
// connection, the prefix under it, and the reader and subscriber on it.
type linkdBinding struct {
	connection config.RedisConnectionConfig
	prefix     string
	client     redis.UniversalClient
	source     *openalerts.SetSource
	subscriber *openalerts.RedisSubscriber
}

// linkdLocationSwitch is the index's source and subscriber, delegating to the
// binding in force. A process whose startup discovery failed, or whose
// Console names a Redis it holds no connection to, is bound nowhere and reads
// nothing: there is no fallback location, since a set read from a place the
// Console did not name answers for nobody. retry keeps asking the Console
// after a failed discovery, and a reconciliation that finds the link writing
// elsewhere hands its target to relocate; either moves every read to the
// place the Console names when this process holds a connection to it. The
// cache needs no restart: a Watch in progress on the old binding returns when
// the binding is replaced, and the cache's own loop subscribes again - on the
// new binding - and rereads every strategy it tracks.
type linkdLocationSwitch struct {
	mu        sync.Mutex
	current   linkdBinding
	replaced  chan struct{}
	discovery fleet.LinkdDiscoveryFacts
	moved     bool

	limits  openalerts.ReadLimits
	console *openalerts.HTTPReconciler
	// open returns the client for a connection: the runtime client when the
	// connection is the runtime's, a new one this switch then owns otherwise.
	open  func(config.RedisConnectionConfig) (redis.UniversalClient, bool)
	owned redis.UniversalClient
	// cfg is the deployment's configuration, for the connections this
	// process holds; calibrate tells the copy the reads moved, so it forgets
	// what the old place held and calibrates at once, and the Console's
	// confirmation does not wait an interval.
	cfg       config.Config
	calibrate func()
}

// bound is whether the binding in force reads anywhere.
func (binding linkdBinding) bound() bool { return binding.source != nil && binding.subscriber != nil }

func newLinkdBinding(client redis.UniversalClient, connection config.RedisConnectionConfig, prefix string,
	limits openalerts.ReadLimits) (linkdBinding, error) {
	source, err := openalerts.NewSetSource(client, prefix, limits)
	if err != nil {
		return linkdBinding{}, err
	}
	subscriber, err := openalerts.NewRedisSubscriber(client, prefix, time.Second)
	if err != nil {
		return linkdBinding{}, err
	}
	return linkdBinding{connection: connection, prefix: prefix, client: client, source: source, subscriber: subscriber}, nil
}

func (s *linkdLocationSwitch) binding() (linkdBinding, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, s.replaced
}

// ReadSet implements openalerts.IndexSource on the binding in force.
func (s *linkdLocationSwitch) ReadSet(ctx context.Context, key openalerts.StrategyKey) ([]string, error) {
	current, _ := s.binding()
	if !current.bound() {
		return nil, openalerts.ErrLocationUnconfirmed
	}
	return current.source.ReadSet(ctx, key)
}

// Watch implements openalerts.Subscriber on the binding in force, and returns
// when that binding is replaced so the cache subscribes again on the new one.
func (s *linkdLocationSwitch) Watch(ctx context.Context, ready func(bool), changed func(openalerts.StrategyKey), refused func(openalerts.NoticeRefusal)) error {
	current, replaced := s.binding()
	if !current.bound() {
		// Nothing to subscribe to until the Console names a place; the
		// cache subscribes again once one is bound.
		select {
		case <-replaced:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	inner, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-replaced:
			cancel()
		case <-inner.Done():
		}
	}()
	err := current.subscriber.Watch(inner, ready, changed, refused)
	if ctx.Err() == nil {
		// Replaced, not stopped: the cache's loop marks the copy not ready and
		// subscribes again, which is the reread the move needs.
		return nil
	}
	return err
}

// Discovery is the location's account for the page: the startup outcome, or
// the one a later retry reached, with every attempt counted.
func (s *linkdLocationSwitch) Discovery() *fleet.LinkdDiscoveryFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	facts := s.discovery
	return &facts
}

// OwnedClient is the client a move opened, for the process to close at
// shutdown; nil when none was opened.
func (s *linkdLocationSwitch) OwnedClient() redis.UniversalClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owned
}

// Location is where the sets are read now, for the endpoint list.
func (s *linkdLocationSwitch) Location() (config.RedisConnectionConfig, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.connection, s.current.prefix, s.moved
}

// rebind moves every read to connection under prefix. The reconciler is told
// first, so the reconciliation that follows the reread checks the new place.
func (s *linkdLocationSwitch) rebind(connection config.RedisConnectionConfig, prefix string) error {
	client, owned := s.open(connection)
	next, err := newLinkdBinding(client, connection, prefix, s.limits)
	if err != nil {
		if owned {
			_ = client.Close()
		}
		return err
	}
	if s.console != nil {
		if err := s.console.SetIndexLocation(openalerts.IndexLocation{KeyPrefix: prefix, Address: linkdLocation(connection),
			Database: connection.DB}); err != nil {
			// Refused before the move: the client opened for it is closed, or
			// every retry would leave one behind.
			if owned {
				_ = client.Close()
			}
			return err
		}
	}
	s.mu.Lock()
	previous := s.owned
	s.current, s.moved = next, true
	if owned {
		s.owned = client
	} else {
		s.owned = nil
	}
	close(s.replaced)
	s.replaced = make(chan struct{})
	calibrate := s.calibrate
	s.mu.Unlock()
	if previous != nil && previous != client {
		_ = previous.Close()
	}
	if calibrate != nil {
		calibrate()
	}
	return nil
}

// relocate is a reconciliation's finding that the link writes somewhere
// other than where this process reads: the target it read is the Console's
// own answer, so the reads move there when this process holds a connection
// to that Redis, and are unbound otherwise - a place the Console no longer
// names is not read on.
func (s *linkdLocationSwitch) relocate(target openalerts.TargetBinding) {
	// The target's own prefix: the configuration's prefix is the one startup
	// adopted, the very place the link no longer writes to, and a prefix the
	// deployment stated is not the Console's word either.
	cfg := s.cfg
	cfg.PhaseTwo.Linkd.KeyPrefix = ""
	connection, prefix, found := heldLinkdLocation(cfg, target)
	s.mu.Lock()
	s.discovery.Target = linkdTargetFacts(target)
	same := found && s.current.bound() && sameRedisConnection(s.current.connection, connection) && s.current.prefix == prefix
	s.mu.Unlock()
	if same {
		return
	}
	if !found {
		s.unbind()
		s.mu.Lock()
		s.discovery.Outcome = fleet.LinkdDiscoveryNoHeldConnection
		s.mu.Unlock()
		return
	}
	if err := s.rebind(connection, prefix); err != nil {
		s.mu.Lock()
		s.discovery.Error = boundedText(err.Error())
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.discovery.Outcome, s.discovery.Error = fleet.LinkdDiscoveryAdopted, ""
	s.mu.Unlock()
}

// unbind stops every read: the place read until now is not the Console's.
func (s *linkdLocationSwitch) unbind() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.current.bound() {
		return
	}
	s.current = linkdBinding{}
	close(s.replaced)
	s.replaced = make(chan struct{})
}

// retry asks the Console where the link writes until it answers, then moves
// the reads there. It runs only for a process whose startup discovery failed:
// an adopted location, a stated connection and a target at a Redis this
// process holds no connection to are all answers, not failures, and none is
// asked again. A reconciliation that finds the link meanwhile moves the reads
// itself (relocate) and is that answer too: the loop stops, and it never
// moves reads already where the Console names - a move forgets everything
// read before it.
func (s *linkdLocationSwitch) retry(ctx context.Context, cfg config.Config, discover discoverLinkdTarget,
	first, ceiling time.Duration) {
	s.mu.Lock()
	failed := s.discovery.Outcome == fleet.LinkdDiscoveryFailed
	s.mu.Unlock()
	if !failed {
		return
	}
	settings := cfg.PhaseTwo.Linkd
	options := openalerts.HTTPReconcilerOptions{BaseURL: settings.ConsoleURL, Username: settings.Username, Password: settings.Password,
		Client: &http.Client{Timeout: 5 * time.Second}, MaxResponseBytes: 1 << 20}
	pause := first
	for {
		if !waitLinkdRetry(ctx, pause) {
			return
		}
		s.mu.Lock()
		failed = s.discovery.Outcome == fleet.LinkdDiscoveryFailed
		s.mu.Unlock()
		if !failed {
			return
		}
		pause = min(2*pause, ceiling)
		target, err := discover(ctx, options)
		s.mu.Lock()
		s.discovery.Attempts++
		noteListedTargets(&s.discovery, target, err)
		if err != nil {
			s.discovery.Error = boundedText(err.Error())
			s.mu.Unlock()
			continue
		}
		s.discovery.Error, s.discovery.Target = "", linkdTargetFacts(target)
		s.mu.Unlock()
		connection, prefix, found := heldLinkdLocation(cfg, target)
		if !found {
			s.mu.Lock()
			s.discovery.Outcome = fleet.LinkdDiscoveryNoHeldConnection
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		same := s.current.bound() && sameRedisConnection(s.current.connection, connection) && s.current.prefix == prefix
		s.mu.Unlock()
		if same {
			s.mu.Lock()
			s.discovery.Outcome = fleet.LinkdDiscoveryAdopted
			s.mu.Unlock()
			return
		}
		if err := s.rebind(connection, prefix); err != nil {
			s.mu.Lock()
			s.discovery.Error = boundedText(err.Error())
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.discovery.Outcome = fleet.LinkdDiscoveryAdopted
		s.mu.Unlock()
		return
	}
}

func waitLinkdRetry(ctx context.Context, pause time.Duration) bool {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// sameRedisConnection is whether two connections are one: the switch then
// reuses the runtime client rather than opening a second.
func sameRedisConnection(left, right config.RedisConnectionConfig) bool {
	return reflect.DeepEqual(left, right)
}
