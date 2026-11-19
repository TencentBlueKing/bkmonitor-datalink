// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"

	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// silentProxy forwards TCP to Redis and, while silent, drops every byte both ways without closing
// anything: the connection stays open and nothing answers, as with a peer that vanished.
type silentProxy struct {
	listener net.Listener
	target   string
	silent   atomic.Bool
	mu       sync.Mutex
	conns    []net.Conn
}

func startSilentProxy(t *testing.T, target string) *silentProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &silentProxy{listener: listener, target: target}
	t.Cleanup(p.close)
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", p.target)
			if err != nil {
				_ = client.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, client, server)
			p.mu.Unlock()
			go p.pipe(server, client)
			go p.pipe(client, server)
		}
	}()
	return p
}

func (p *silentProxy) pipe(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.silent.Load() {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *silentProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.conns {
		_ = conn.Close()
	}
}

// waitState waits for the subscription to report want, and fails if it reports nothing of the kind.
func waitState(t *testing.T, states <-chan bool, want bool, within time.Duration) {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case got := <-states:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("subscription did not report up=%v within %s", want, within)
		}
	}
}

func TestSubscriptionReportsWhetherItIsLive(t *testing.T) {
	server := miniredis.RunT(t)
	proxy := startSilentProxy(t, server.Addr())
	client := goRedis.NewClient(&goRedis.Options{Addr: proxy.listener.Addr().String(), DialTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan bool, 1024)
	messages := routerInfluxdb.NewRouter("sync", client).SubscribeChannelsWithState(
		ctx, func(up bool) { states <- up }, "table:channel",
	)

	waitState(t, states, true, 5*time.Second)
	if receivers := server.Publish("sync:table:channel", "first"); receivers != 1 {
		t.Fatalf("want one subscriber, got %d", receivers)
	}
	select {
	case msg := <-messages:
		if msg.Channel != "sync:table:channel" || msg.Payload != "first" {
			t.Fatalf("unexpected message %s %s", msg.Channel, msg.Payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("published message not delivered")
	}

	// Idle across several health checks: every ping is answered and nothing goes down.
	time.Sleep(8 * time.Second)
	for len(states) > 0 {
		if !<-states {
			t.Fatal("an idle subscription whose pings are answered went down")
		}
	}

	// The connection stays open and stops answering: the unanswered ping takes it down, and the
	// subscription comes back on a new connection once Redis answers again.
	proxy.silent.Store(true)
	waitState(t, states, false, 12*time.Second)
	proxy.silent.Store(false)
	waitState(t, states, true, 15*time.Second)
	server.Publish("sync:table:channel", "second")
	select {
	case msg := <-messages:
		if msg.Payload != "second" {
			t.Fatalf("unexpected payload %s", msg.Payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message after recovery not delivered")
	}

	// Redis goes away and comes back on the same address.
	server.Close()
	waitState(t, states, false, 5*time.Second)
	if err := server.Restart(); err != nil {
		t.Fatal(err)
	}
	waitState(t, states, true, 10*time.Second)

	// Stopping is not an outage: the channel closes and nothing is reported.
	for len(states) > 0 {
		<-states
	}
	cancel()
	select {
	case _, open := <-messages:
		for open {
			_, open = <-messages
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the message channel did not close after cancel")
	}
	time.Sleep(200 * time.Millisecond)
	if len(states) != 0 {
		t.Fatalf("a stopped subscription reported %v", <-states)
	}
}
