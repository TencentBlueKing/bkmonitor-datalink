// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production Worker finds its own Leader from the records, connects
// over the listener's port, installs the view and reports it -- and none
// of it touches execution (decision-016 section 7.1, the shadow; the
// Leader's hard condition one). The stream is then taken away, refused,
// and given back, and through all of it Slots complete FULL and the cursor
// advances exactly as they did with the stream up.
func TestTheProductionWorkerShadowsTheViewAndExecutionNeverNotices(t *testing.T) {
	// The listener the registration will advertise, served here: h2c to the
	// bundle's control stream once the bundle exists, refused or gone when
	// the test says so.
	address := reserveAddressForBundle(t)
	var stream atomic.Pointer[http.Handler]
	var refuse atomic.Bool
	handler := h2c.NewHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if refuse.Load() {
			http.Error(response, "gone", http.StatusServiceUnavailable)
			return
		}
		if current := stream.Load(); current != nil && request.ProtoMajor == 2 && strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
			(*current).ServeHTTP(response, request)
			return
		}
		http.NotFound(response, request)
	}), &http2.Server{})
	// Connections are tracked so the test can cut them: h2c hijacks the
	// connection, and http.Server.Close does not reach a hijacked one.
	tracked, err := listenTracked(address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(tracked) }()
	t.Cleanup(func() { _ = server.Close(); tracked.closeAll() })

	fixture := startCutoverFixture(t, func(cfg *config.Config) { cfg.HTTP.Listen = address })
	ctx := context.Background()
	bundle := fixture.bundle
	stream.Store(&bundle.dependencies.ControlStream)
	client := bundle.dependencies.ViewClient
	if client == nil {
		t.Fatal("the production bundle has no view client")
	}
	// The registration advertises this listener; the Worker finds itself as
	// Leader there and installs its own projection: both Query Groups.
	waitFor(t, "the Worker installs the view from its own Leader", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision >= 1 && len(view.Entries) == 2
	})
	stats := client.Stats()
	if !stats.Connected || stats.Leader.WorkerID != fixture.cfg.PhaseTwo.Worker.ID || !strings.HasSuffix(stats.Leader.Endpoint, address[strings.LastIndex(address, ":"):]) || stats.ObjectsMissing != 0 {
		t.Fatalf("client stats = %+v, want connected to this replica at the listener with no object missing", stats)
	}
	waitFor(t, "the Leader counts the install", func() bool {
		leader := bundle.dependencies.ViewStreamStats()
		return leader.Counts.Installed == 1 && leader.Sessions == 1
	})

	// Execution with the stream up: the next Slot completes FULL.
	withStream := runOneSlotFull(t, fixture)

	// The stream is taken away: the Worker is disconnected and keeps trying;
	// the next Slot completes FULL all the same, the cursor moves the same.
	stream.Store(nil)
	refuse.Store(true)
	_ = server.Close()
	tracked.closeAll()
	waitFor(t, "the Worker notices the stream is gone", func() bool { return !client.Stats().Connected })
	withoutStream := runOneSlotFull(t, fixture)
	if withoutStream.CompletionKind != withStream.CompletionKind || withoutStream.Result != withStream.Result || withoutStream.ReasonCode != withStream.ReasonCode {
		t.Fatalf("Slot without the stream = %+v, with it %+v: the stream changed execution", withoutStream, withStream)
	}
	if progress := fixture.progress(ctx); progress.NextSlot <= withStream.slot {
		t.Fatalf("the cursor did not advance past the Slot run with the stream: %+v", progress)
	}
	// The listener returns but refuses: still nothing changes for execution.
	tracked, err = listenTracked(address)
	if err != nil {
		t.Fatal(err)
	}
	server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(tracked) }()
	t.Cleanup(func() { _ = server.Close(); tracked.closeAll() })
	waitFor(t, "the Worker keeps trying", func() bool { return client.Stats().Connections >= 1 && !client.Stats().Connected })
	refused := runOneSlotFull(t, fixture)
	if refused.CompletionKind != withStream.CompletionKind {
		t.Fatalf("Slot while refused = %+v, want %+v", refused, withStream)
	}
	// Given back: the Worker reconnects and installs again, by snapshot.
	refuse.Store(false)
	stream.Store(&bundle.dependencies.ControlStream)
	waitFor(t, "the Worker reconnects", func() bool {
		stats := client.Stats()
		return stats.Connected && stats.Connections >= 2
	})
}

type slotOutcome struct {
	slot           execution.EvaluationTime
	CompletionKind execution.CompletionKind
	Result         observability.Result
	ReasonCode     execution.ReasonCode
}

// runOneSlotFull advances the fixture's clock to the next grid point and
// runs the Runner until one Slot completes FULL, returning what it said.
func runOneSlotFull(t *testing.T, fixture *cutoverStallFixture) slotOutcome {
	t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 200; attempt++ {
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		at := fixture.now()
		result, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("RunOne: %v", err)
		}
		if !attempted {
			next := at.Unix() - at.Unix()%60 + 60
			fixture.clock.Store(next*1000 + 1500)
			continue
		}
		if result.Completed && result.CompletionKind == execution.CompletionFull {
			return slotOutcome{slot: fixture.progress(ctx).LastFullSlot, CompletionKind: result.CompletionKind, Result: result.Result, ReasonCode: result.ReasonCode}
		}
		fixture.clock.Store(at.Add(time.Second).UnixMilli())
	}
	t.Fatal("no FULL Slot within 200 attempts")
	return slotOutcome{}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within ten seconds", what)
}

// trackedListener remembers every connection it accepted so a test can
// close them all, hijacked ones included.
type trackedListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func listenTracked(address string) (*trackedListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &trackedListener{Listener: listener}, nil
}

func (listener *trackedListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	listener.mu.Lock()
	listener.conns = append(listener.conns, conn)
	listener.mu.Unlock()
	return conn, nil
}

func (listener *trackedListener) closeAll() {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	for _, conn := range listener.conns {
		_ = conn.Close()
	}
	listener.conns = nil
}

func reserveAddressForBundle(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// The execution path does not import the view stream: the hard condition
// four of decision-016's shadow step, pinned on the import graph rather
// than on one behaviour. A package on the Slot's path that starts to
// depend on viewstream fails here before it can read a view.
func TestTheHotPathDoesNotImportTheViewStream(t *testing.T) {
	const module = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/"
	for _, pkg := range []string{"scheduler", "worker", "controlplane", "execution", "state", "progress", "kafka", "ownership", "evaluation", "access"} {
		out, err := exec.Command("go", "list", "-deps", module+pkg).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", pkg, err)
		}
		for _, dep := range strings.Split(string(out), "\n") {
			if strings.HasSuffix(dep, "/pkg/alarmd/viewstream") || strings.HasSuffix(dep, "/pkg/alarmd/viewstream/pb") {
				t.Fatalf("%s depends on %s: the hot path must not read the view before the cutover decision", pkg, dep)
			}
		}
	}
}
