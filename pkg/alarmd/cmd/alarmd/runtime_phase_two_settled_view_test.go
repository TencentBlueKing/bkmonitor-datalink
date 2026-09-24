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
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// runScheduledOnceSettled runs one scheduler pass once the Worker's
// executable view has caught up with its records: for every owned Query
// Group the installed view carries the entry with content and a timeline
// revision, the Assignment record says the same revision, and the session's
// lease has brought it - renewing the lease when it has not, since a
// fixture's renewal interval is a minute and the accepted lag is one renewal.
//
// This is the wait a production Worker pays after a rollout or a cutover
// (decision-016 batch 4b, the accepted bound: renewal interval plus delta
// propagation); a fixture that ran the pass at once ran it inside that
// window and got view_not_executable, which is the answer it would get, not
// the one the case is about. Bounded: past the deadline the pass runs
// anyway, so a case whose view never settles fails on its own assertion
// with the round's word on it rather than here.
func runScheduledOnceSettled(ctx context.Context, bundle *phaseTwoWorkerBundle) error {
	settleExecutableView(ctx, bundle, 10*time.Second)
	return bundle.runScheduledOnce(ctx)
}

// controlStreamServers is the control stream listener each bundle under test
// got, keyed by bundle: a fixture that only Start()s the bundle serves no
// listener, and the Worker's own view client has nothing to install from
// until one is served on the address its registration advertises. Kept for
// the life of the test process rather than closed per case, because the
// helper has no test to hang a cleanup on. A case about a Worker whose
// Leader is unreachable withholds it (withholdControlStreamForTest), and
// the settling helpers leave it withheld until the case gives it back.
var controlStreamServers sync.Map

type testControlStream struct {
	server   *http.Server
	listener *trackedListener
	withheld bool
}

func (stream *testControlStream) stop() {
	if stream.server != nil {
		_ = stream.server.Close()
		stream.listener.closeAll()
	}
}

func reserveBundleAddress() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "127.0.0.1:1"
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func serveControlStreamForTest(bundle *phaseTwoWorkerBundle) {
	if bundle.dependencies.ControlStream == nil {
		return
	}
	if _, served := controlStreamServers.Load(bundle); served {
		return
	}
	// Connections are tracked so a case can cut them: h2c hijacks the
	// connection, and http.Server.Close does not reach a hijacked one.
	listener, err := listenTracked(bundle.dependencies.Config.HTTP.Listen)
	if err != nil {
		return
	}
	handler := h2c.NewHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor == 2 && strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
			bundle.dependencies.ControlStream.ServeHTTP(response, request)
			return
		}
		http.NotFound(response, request)
	}), &http2.Server{})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if _, raced := controlStreamServers.LoadOrStore(bundle, &testControlStream{server: server, listener: listener}); raced {
		_ = listener.Close()
		return
	}
	go func() { _ = server.Serve(listener) }()
}

// withholdControlStreamForTest takes the bundle's control stream away --
// listener and every connection on it -- and keeps the settling helpers
// from standing it up again until restoreControlStreamForTest.
func withholdControlStreamForTest(bundle *phaseTwoWorkerBundle) {
	if previous, served := controlStreamServers.Swap(bundle, &testControlStream{withheld: true}); served {
		previous.(*testControlStream).stop()
	}
}

func restoreControlStreamForTest(bundle *phaseTwoWorkerBundle) {
	controlStreamServers.Delete(bundle)
	serveControlStreamForTest(bundle)
}

func settleExecutableView(ctx context.Context, bundle *phaseTwoWorkerBundle, wait time.Duration) bool {
	client := bundle.dependencies.ViewClient
	ownership, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if client == nil || !ok {
		return true
	}
	serveControlStreamForTest(bundle)
	deadline := time.Now().Add(wait)
	for {
		renewLapsedRegistrationForTest(ctx, bundle, ownership)
		if viewSettled(ctx, bundle, ownership) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// renewLapsedRegistrationForTest re-registers the Worker when its own
// registration has lapsed by the bundle's clock. A fixture moves that clock
// by half an hour in one store; the renewal loop runs on the wall clock and
// takes an interval to notice, while the stream's admission reads the
// registration by the moved clock and refuses the Worker's own session as
// unknown until then. A production clock never moves past the registration
// TTL between two renewals; this is the renewal the loop would have run.
func renewLapsedRegistrationForTest(ctx context.Context, bundle *phaseTwoWorkerBundle, production *productionPhaseTwoOwnership) {
	registry, ok := production.dependencies.Store.(viewStreamRegistry)
	if !ok {
		return
	}
	registration, found, err := registry.ReadWorker(ctx, bundle.dependencies.Config.PhaseTwo.Worker.ID)
	if err != nil || (found && registration.ExpiresAt.After(bundle.dependencies.Now())) {
		return
	}
	_ = bundle.register(ctx, ownership.WorkerReady)
}

func viewSettled(ctx context.Context, bundle *phaseTwoWorkerBundle, ownership *productionPhaseTwoOwnership) bool {
	bundle.mu.RLock()
	groups := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
	runners := make(map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime, len(bundle.runners))
	for group, lifecycle := range bundle.runners {
		runners[group] = lifecycle.runner
	}
	bundle.mu.RUnlock()
	for _, group := range groups {
		entry, inView := bundle.dependencies.ViewClient.Entry(group)
		if !inView {
			return false
		}
		// In the view without content: draining with nothing left to run, or
		// a Segment that names none. The view has spoken; there is nothing to
		// wait for, and a case about that Query Group reads its own word.
		if entry.Content == nil {
			continue
		}
		if entry.Assignment.TimelineRecordRevision == 0 {
			return false
		}
		record, err := ownership.dependencies.Store.ReadAssignment(ctx, group)
		if err != nil || record.TimelineRecordRevision != entry.Assignment.TimelineRecordRevision {
			return false
		}
		runtime, opened := runners[group].(*productionPhaseTwoQueryGroup)
		if !opened {
			continue
		}
		lease, held := runtime.session.Current()
		if !held {
			return false
		}
		if lease.TimelineRecordRevision != record.TimelineRecordRevision || lease.ContentScope != record.ContentScope {
			ttl := bundle.dependencies.Config.PhaseTwo.Ownership.LeaseTTL.Duration()
			if err := runtime.session.Renew(ctx, runtime.clock(), ttl); err != nil {
				return false
			}
			lease, _ = runtime.session.Current()
			if lease.TimelineRecordRevision != record.TimelineRecordRevision {
				return false
			}
		}
	}
	return true
}

// settledRunner is a fixture's handle on one of the bundle's Query Group
// runtimes that settles the executable view before each round, the way the
// scheduled pass does through runScheduledOnceSettled. A case that runs a
// round by hand runs it inside the same window a production Worker waits
// out after a rollout or a cutover, and would read view_not_executable
// where the case is about something else. Everything but RunOne is the
// runtime's own.
func settledRunner(bundle *phaseTwoWorkerBundle, queryGroup execution.QueryGroupIdentity) phaseTwoQueryGroupRuntime {
	bundle.mu.RLock()
	lifecycle := bundle.runners[queryGroup]
	bundle.mu.RUnlock()
	if lifecycle == nil {
		return nil
	}
	return &settlingRuntime{phaseTwoQueryGroupRuntime: lifecycle.runner, bundle: bundle}
}

type settlingRuntime struct {
	phaseTwoQueryGroupRuntime
	bundle *phaseTwoWorkerBundle
}

func (runtime *settlingRuntime) RunOne(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
	settleExecutableView(ctx, runtime.bundle, 10*time.Second)
	return runtime.phaseTwoQueryGroupRuntime.RunOne(ctx)
}

func (runtime *settlingRuntime) RunOneAdmitted(ctx context.Context, admission scheduler.ExecutionAdmission) (execution.SlotExecutionResult, bool, bool, error) {
	settleExecutableView(ctx, runtime.bundle, 10*time.Second)
	return runtime.phaseTwoQueryGroupRuntime.RunOneAdmitted(ctx, admission)
}
