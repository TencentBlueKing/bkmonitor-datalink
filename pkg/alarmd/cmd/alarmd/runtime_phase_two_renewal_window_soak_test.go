// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build soak

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// loadingRedisProxy stands between a client and a real redis-server, and while
// it is told to answers what a server reading its dataset back answers.
type loadingRedisProxy struct {
	target  string
	address string
	loading atomic.Bool

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
}

func startLoadingRedisProxy(t *testing.T, target string) *loadingRedisProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &loadingRedisProxy{target: target, address: listener.Addr().String(), listener: listener, conns: make(map[net.Conn]struct{})}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			proxy.mu.Lock()
			proxy.conns[conn] = struct{}{}
			proxy.mu.Unlock()
			go proxy.serve(conn)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.cut()
	})
	return proxy
}

// cut closes every connection, as the restart that starts the loading does.
func (proxy *loadingRedisProxy) cut() {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	for conn := range proxy.conns {
		_ = conn.Close()
		delete(proxy.conns, conn)
	}
}

func (proxy *loadingRedisProxy) serve(client net.Conn) {
	defer func() { _ = client.Close() }()
	reader := bufio.NewReaderSize(client, 1<<16)
	var upstream net.Conn
	defer func() {
		if upstream != nil {
			_ = upstream.Close()
		}
	}()
	for {
		raw, name, err := readRawRedisCommand(reader)
		if err != nil {
			return
		}
		// What the client sends to set a connection up still runs while loading.
		if proxy.loading.Load() && name != "AUTH" && name != "SELECT" && name != "HELLO" {
			if _, err := client.Write([]byte("-LOADING Redis is loading the dataset in memory\r\n")); err != nil {
				return
			}
			continue
		}
		if upstream == nil {
			upstream, err = net.Dial("tcp", proxy.target)
			if err != nil {
				return
			}
			go func(from net.Conn) {
				_, _ = io.Copy(client, from)
				_ = client.Close()
			}(upstream)
		}
		if _, err := upstream.Write(raw); err != nil {
			return
		}
	}
}

// readRawRedisCommand reads one RESP array command: its bytes and its upper-cased
// name.
func readRawRedisCommand(reader *bufio.Reader) ([]byte, string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, "", err
	}
	if len(header) < 2 || header[0] != '*' {
		return nil, "", fmt.Errorf("not a RESP array: %q", header)
	}
	count, err := strconv.Atoi(strings.TrimSpace(header[1:]))
	if err != nil {
		return nil, "", err
	}
	raw := []byte(header)
	name := ""
	for index := 0; index < count; index++ {
		size, err := reader.ReadString('\n')
		if err != nil {
			return nil, "", err
		}
		length, err := strconv.Atoi(strings.TrimSpace(size[1:]))
		if err != nil {
			return nil, "", err
		}
		body := make([]byte, length+2)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, "", err
		}
		raw = append(append(raw, size...), body...)
		if index == 0 {
			name = strings.ToUpper(string(body[:length]))
		}
	}
	return raw, name, nil
}

// renewalWatch keeps when each renewal of a lease reached the store.
type renewalWatch struct {
	mu      sync.Mutex
	renewed []time.Time
}

func (watch *renewalWatch) observe(_ context.Context, observation observability.Observation) {
	if observation.Stage != observability.StageLeaseRenewed || observation.Result != observability.ResultSuccess {
		return
	}
	watch.mu.Lock()
	watch.renewed = append(watch.renewed, time.Now())
	watch.mu.Unlock()
}

func (watch *renewalWatch) count() int {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return len(watch.renewed)
}

func (watch *renewalWatch) at(index int) time.Time {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.renewed[index]
}

// A lease survives a failure that ends before the last attempt inside its TTL,
// and that attempt is what saves it. All three renewal loops share the round; the
// two whose TTL is three intervals are the ones the spacing decides. It runs on
// the real scripts behind a proxy answering LOADING, a failure that returns at
// once as the one that is hardest on a loop that waits between attempts.
func TestALeaseSurvivesAFailureEndingBeforeItsLastAttempt(t *testing.T) {
	const (
		interval = 2 * time.Second
		ttl      = 3 * interval
		// The last renewal plus this much is where the failure ends. The attempts
		// bunched at the start of each interval ended at 4.4 s and left nobody
		// asking until 6 s, the TTL itself. Spread, they end at 5.33 s.
		failureEnds = 4850 * time.Millisecond
		// Well before the 6 s tick, so a renewal that only the next round could
		// make does not pass.
		renewedBy = 5700 * time.Millisecond
	)
	for name, start := range map[string]func(
		*testing.T, context.Context, *ownership.RedisStore, ownership.PublicationAuthority, observability.Observer,
	) <-chan error{
		"query group": func(
			t *testing.T, ctx context.Context, store *ownership.RedisStore, authority ownership.PublicationAuthority, observer observability.Observer,
		) <-chan error {
			now := time.Now()
			queryGroup := execution.QueryGroupIdentity("query-group-1")
			if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
				QueryGroup: queryGroup, DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			session, err := ownership.OpenSession(ctx, store, queryGroup, "worker-1", now, ttl)
			if err != nil {
				t.Fatal(err)
			}
			runtime := &productionPhaseTwoQueryGroup{queryGroup: queryGroup, session: session, observer: observer, now: time.Now}
			done := make(chan error, 1)
			go func() { done <- runtime.MaintainLease(ctx, interval, ttl) }()
			return done
		},
		"control leader": func(
			_ *testing.T, ctx context.Context, store *ownership.RedisStore, authority ownership.PublicationAuthority, observer observability.Observer,
		) <-chan error {
			runtime := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil),
				dependencies: productionPhaseTwoOwnershipDependencies{Store: store, Observer: observer, Now: time.Now},
				authority:    authority,
			}
			done := make(chan error, 1)
			go func() { done <- runtime.MaintainControlLeader(ctx, interval, ttl) }()
			return done
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := redistest.Start(t)
			proxy := startLoadingRedisProxy(t, server.Addr)
			client := redis.NewClient(&redis.Options{Addr: proxy.address, MaxRetries: -1, DialTimeout: time.Second,
				ReadTimeout: time.Second, WriteTimeout: time.Second})
			t.Cleanup(func() { _ = client.Close() })
			store, err := ownership.NewRedisStoreWithClient(client, "alarmd-renewal-window")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority, err := store.AcquireControlLeader(ctx, "control-1", time.Now(), ttl)
			if err != nil {
				t.Fatal(err)
			}
			watch := &renewalWatch{}
			done := start(t, ctx, store, authority, observability.ObserverFunc(watch.observe))

			testwait.Until(t, "the first renewal", func() bool { return watch.count() > 0 })
			last := watch.at(0)
			proxy.cut()
			proxy.loading.Store(true)
			time.Sleep(time.Until(last.Add(failureEnds))) // fixed-wait: the failure lasts until this instant
			if count := watch.count(); count != 1 {
				t.Fatalf("%d renewals reached a store answering LOADING", count)
			}
			proxy.loading.Store(false)

			testwait.Until(t, "a renewal after the failure", func() bool {
				select {
				case err := <-done:
					t.Fatalf("the lease was given up %s after its last renewal: %v", time.Since(last), err)
				default:
				}
				return watch.count() > 1
			})
			if renewedAfter := watch.at(1).Sub(last); renewedAfter > renewedBy {
				t.Fatalf("the lease was renewed %s after its last renewal, want by %s: that is the next round, not the last attempt",
					renewedAfter, renewedBy)
			}
		})
	}
}
