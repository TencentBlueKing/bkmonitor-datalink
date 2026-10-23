// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// A Worker that started in a Leader gap keeps failing to connect and backs
// off toward ReconnectMax. When a Leader of a new term appears it connects
// within one ReconnectMin of it, not at the end of a backoff chosen while
// there was nobody to connect to: the gap is the control plane's, and the
// view lag after it is bounded by the renewal interval plus the delta's
// propagation (B4 §2), not by the reconnect ceiling. Time here is the
// client's own: each wait it asks for moves the clock by that much.
func TestAWorkerConnectsWithinAReconnectMinOfANewLeaderTerm(t *testing.T) {
	harness := startServer(t)
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.server.Publish(context.Background(), desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})); err != nil {
		t.Fatal(err)
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader-new", harness.server)
	// The old term names an endpoint nothing listens on: every connect fails.
	discovery := &scriptedDiscovery{}
	discovery.leader, discovery.found = viewstream.LeaderEndpoint{WorkerID: "leader-old", ControlEpoch: 6, Endpoint: "leader-gone"}, true
	var clock atomic.Int64
	clock.Store(time.Unix(1_700_000_000, 0).UnixMilli())
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1", StreamToken: "t1"}, discovery, nil, &sessionObserver{},
		viewstream.ClientOptions{Dial: dialer.dial, Now: func() time.Time { return time.UnixMilli(clock.Load()) }, Tick: time.Hour,
			Sleep: func(ctx context.Context, wait time.Duration) error {
				// The wait passes, then the clock shows it: a Leader that
				// appears during a wait appears before the wait is over.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Millisecond):
				}
				clock.Add(wait.Milliseconds())
				return nil
			}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()
	// Long enough failing that the backoff ceiling is ReconnectMax.
	eventually(t, "the backoff grows past its floor", func() bool {
		return time.UnixMilli(clock.Load()).Sub(time.Unix(1_700_000_000, 0)) > 2*viewstream.ReconnectMax
	})
	discovery.mu.Lock()
	discovery.leader = viewstream.LeaderEndpoint{WorkerID: "leader-new", ControlEpoch: 7, Endpoint: "leader-new"}
	switched := clock.Load()
	discovery.mu.Unlock()
	eventually(t, "the Worker installs from the new term", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 1
	})
	if waited := time.Duration(clock.Load()-switched) * time.Millisecond; waited > 2*viewstream.ReconnectMin {
		t.Fatalf("the Worker connected %s after the new term appeared, want within %s", waited, viewstream.ReconnectMin)
	}
}
