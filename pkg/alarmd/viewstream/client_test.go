// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// scriptedDiscovery answers with whatever endpoint the test set last.
type scriptedDiscovery struct {
	mu     sync.Mutex
	leader viewstream.LeaderEndpoint
	found  bool
	err    error
	asked  int
	// miss, when set, is the named way this discovery found no Leader.
	miss string
}

func (discovery *scriptedDiscovery) Leader(context.Context) (viewstream.LeaderEndpoint, string, error) {
	discovery.mu.Lock()
	defer discovery.mu.Unlock()
	discovery.asked++
	if discovery.err != nil {
		return viewstream.LeaderEndpoint{}, "", discovery.err
	}
	if discovery.miss != "" {
		return viewstream.LeaderEndpoint{}, discovery.miss, nil
	}
	if !discovery.found {
		return viewstream.LeaderEndpoint{}, viewstream.MissNoLeader, nil
	}
	return discovery.leader, "", nil
}

func (discovery *scriptedDiscovery) set(endpoint string, found bool) {
	discovery.mu.Lock()
	defer discovery.mu.Unlock()
	discovery.leader, discovery.found = viewstream.LeaderEndpoint{WorkerID: "leader", ControlEpoch: 7, Endpoint: endpoint}, found
}

// countingProbe answers from a set of digests it holds to be missing --
// the count is derived from what it is asked, as the catalog's would be --
// and remembers what it was asked about. An error set makes it fail.
type countingProbe struct {
	mu      sync.Mutex
	missing map[string]struct{}
	err     error
	asked   [][]execution.ObjectDigest
	// hold, when set, blocks the next call that asks about holdFor until
	// released, once; entered is closed when that call begins. Keyed on an
	// object so the hold catches the call it means: an install probes its
	// own view in the same stretch as a heartbeat re-probes the old one, and
	// a hold on "the next call" caught whichever came first. The held call
	// also gives up with its context, as the catalog's own read does, so a
	// test that fails while holding it does not leave the client stuck in
	// its cleanup until the test binary times out.
	hold, entered chan struct{}
	holdFor       string
}

func asksAbout(objects []execution.ObjectDigest, digest string) bool {
	for _, object := range objects {
		if string(object) == digest {
			return true
		}
	}
	return false
}

func (probe *countingProbe) MissingObjects(ctx context.Context, objects []execution.ObjectDigest, contexts []execution.OutputContextDigest) (int, error) {
	probe.mu.Lock()
	var hold, entered chan struct{}
	if probe.hold != nil && (probe.holdFor == "" || asksAbout(objects, probe.holdFor)) {
		hold, entered = probe.hold, probe.entered
		probe.hold, probe.entered, probe.holdFor = nil, nil, ""
	}
	probe.mu.Unlock()
	if hold != nil {
		close(entered)
		select {
		case <-hold:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.asked = append(probe.asked, append([]execution.ObjectDigest(nil), objects...))
	if probe.err != nil {
		return 0, probe.err
	}
	count := 0
	for _, digest := range objects {
		if _, gone := probe.missing[string(digest)]; gone {
			count++
		}
	}
	for _, digest := range contexts {
		if _, gone := probe.missing[string(digest)]; gone {
			count++
		}
	}
	return count, nil
}

// bufconnDialer dials whichever listener the endpoint names, so a test can
// run two Leaders and move the client between them.
type bufconnDialer struct {
	mu        sync.Mutex
	listeners map[string]*bufconn.Listener
	servers   map[string]*grpc.Server
}

func (dialer *bufconnDialer) dial(_ context.Context, endpoint string) (pb.ControlServiceClient, func() error, error) {
	dialer.mu.Lock()
	listener, ok := dialer.listeners[endpoint]
	dialer.mu.Unlock()
	if !ok {
		return nil, nil, errors.New("no listener at " + endpoint)
	}
	conn, err := grpc.NewClient("passthrough:///"+endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewControlServiceClient(conn), conn.Close, nil
}

// serveOn puts a ControlService behind a bufconn listener under a name. A
// name already served is taken over: the previous server is stopped, which
// ends every stream on it, the way a Leader restart does.
func (dialer *bufconnDialer) serveOn(t *testing.T, name string, service pb.ControlServiceServer) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pb.RegisterControlServiceServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	dialer.mu.Lock()
	if dialer.listeners == nil {
		dialer.listeners = map[string]*bufconn.Listener{}
		dialer.servers = map[string]*grpc.Server{}
	}
	previous := dialer.servers[name]
	dialer.listeners[name], dialer.servers[name] = listener, grpcServer
	dialer.mu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

func startClient(t *testing.T, discovery *scriptedDiscovery, probe viewstream.ObjectProbe, dialer *bufconnDialer, observer *sessionObserver) (*viewstream.Client, context.CancelFunc) {
	t.Helper()
	return startClientWithClock(t, discovery, probe, dialer, observer, nil)
}

func startClientWithClock(t *testing.T, discovery *scriptedDiscovery, probe viewstream.ObjectProbe, dialer *bufconnDialer, observer *sessionObserver, clock *atomic.Int64) (*viewstream.Client, context.CancelFunc) {
	t.Helper()
	options := viewstream.ClientOptions{Dial: dialer.dial}
	if clock != nil {
		options.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
		options.Tick = 20 * time.Millisecond
	}
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1", StreamToken: "t1"}, discovery, probe, observer,
		viewstream.ClientOptions{Dial: dialer.dial, Now: options.Now, Tick: options.Tick, Sleep: func(ctx context.Context, wait time.Duration) error {
			// A test does not wait out the jitter; it waits a tick so the loop
			// cannot spin.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
				return nil
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return client, cancel
}

// The Worker finds the Leader, installs the snapshot it is sent and
// reports it; a publication that moves its projection arrives as a delta
// and is installed on top; one that does not arrives as an empty delta and
// is installed by receipt; the Leader's ledger counts each install. The
// objects missing are counted over the whole installed view at every
// install: two missing since the snapshot stay two through a delta that
// touches another Query Group, and a probe that fails reports unknown,
// not 0.
func TestTheWorkerInstallsWhatTheLeaderSendsAndReportsIt(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	first := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	if _, err := harness.server.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader-a", harness.server)
	discovery := &scriptedDiscovery{}
	discovery.set("leader-a", true)
	// qg-2's object and its context are missing from the catalog.
	probe := &countingProbe{missing: map[string]struct{}{"obj-2": {}, "ctx-s2": {}}}
	observer := &sessionObserver{}
	client, _ := startClient(t, discovery, probe, dialer, observer)

	eventually(t, "the snapshot is installed", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 1 && len(view.Entries) == 2
	})
	eventually(t, "the Leader counts the install", func() bool {
		return harness.server.Stats().Counts.Installed == 1
	})
	stats := client.Stats()
	if !stats.Connected || stats.Leader.Endpoint != "leader-a" || stats.Installs["snapshot"] != 1 || stats.ObjectsMissing != 2 || !stats.ObjectsProbed || stats.Installed.Revision != 1 {
		t.Fatalf("client stats after the snapshot = %+v", stats)
	}
	probe.mu.Lock()
	asked := len(probe.asked)
	firstAsk := probe.asked[0]
	probe.mu.Unlock()
	if asked != 1 || len(firstAsk) != 2 {
		t.Fatalf("probe asked %d times, first about %v; want once about the snapshot's two objects", asked, firstAsk)
	}
	eventually(t, "the Leader holds the count", func() bool {
		for _, lagging := range harness.server.Stats().Lagging {
			_ = lagging
		}
		return harness.server.Stats().Counts.Installed == 1
	})

	// w1's other Query Group moves: a delta with one upsert. The probe is
	// asked about the whole view again, and qg-2's two objects are still
	// missing: a count over the upsert alone would have read 0 here.
	second := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	if _, err := harness.server.Publish(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the delta is installed", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 2
	})
	view, _ := client.Installed()
	if view.Entries[0].Content.ObjectDigest != "obj-1b" || len(view.Entries) != 2 {
		t.Fatalf("view after the delta = %+v", view)
	}
	if stats := client.Stats(); stats.ObjectsMissing != 2 || !stats.ObjectsProbed {
		t.Fatalf("objects missing after a delta on another Query Group = %+v, want the two still missing", stats)
	}
	probe.mu.Lock()
	lastAsk := probe.asked[len(probe.asked)-1]
	probe.mu.Unlock()
	if len(lastAsk) != 2 {
		t.Fatalf("probe asked about %v for the delta, want the whole view's two objects", lastAsk)
	}
	eventually(t, "revision 2 installed on the Leader", func() bool {
		stats := harness.server.Stats()
		return stats.Revision == 2 && stats.Counts.Installed == 1
	})
	// The missing objects arrive in the catalog; only w2 moves: w1 gets an
	// empty delta, installs revision 3 by receipt, and the probe over the
	// whole view now finds nothing missing.
	probe.mu.Lock()
	probe.missing = map[string]struct{}{}
	probe.mu.Unlock()
	third := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3b", "s3")})
	if _, err := harness.server.Publish(ctx, third); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the empty delta is installed", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 3
	})
	if stats := client.Stats(); stats.Installs["snapshot"] != 1 || stats.Installs["delta"] != 1 || stats.Installs["empty_delta"] != 1 || stats.Installed.Revision != 3 ||
		len(stats.InstallFailures) != 0 || stats.SnapshotsRequested != 0 || stats.ObjectsMissing != 0 || !stats.ObjectsProbed {
		t.Fatalf("client stats after three installs = %+v", stats)
	}
	// The probe fails on the next install: the count is unknown, reported
	// as not probed, and the last known number is not carried forward.
	probe.mu.Lock()
	probe.err = errors.New("catalog unreachable")
	probe.mu.Unlock()
	fourth := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1c", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3b", "s3")})
	if _, err := harness.server.Publish(ctx, fourth); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revision 4 installed with the probe failing", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 4
	})
	if stats := client.Stats(); stats.ObjectsProbed || stats.ObjectsMissing != 0 {
		t.Fatalf("stats with a failing probe = %+v, want not probed", stats)
	}
	if observer.count("installed", "OBJECTS_NOT_PROBED") != 1 || observer.count("object_probe_failed", "") != 1 || observer.count("installed", "") != 4 || observer.count("connected", "") != 1 {
		t.Fatalf("events = %+v", observer.events)
	}
}

// scriptedLeader is a ControlService a test writes the lines of: it sends
// what it is told to when a Hello arrives, and records what the Worker
// sends back.
type scriptedLeader struct {
	pb.UnimplementedControlServiceServer
	mu       sync.Mutex
	onHello  []*pb.LeaderMessage
	received []*pb.WorkerMessage
	replies  func(message *pb.WorkerMessage) []*pb.LeaderMessage
}

func (leader *scriptedLeader) Connect(stream pb.ControlService_ConnectServer) error {
	for {
		message, err := stream.Recv()
		if err == io.EOF || err != nil {
			return nil
		}
		leader.mu.Lock()
		leader.received = append(leader.received, message)
		var out []*pb.LeaderMessage
		if message.GetHello() != nil {
			out = append(out, leader.onHello...)
		}
		if leader.replies != nil {
			out = append(out, leader.replies(message)...)
		}
		leader.mu.Unlock()
		for _, reply := range out {
			if err := stream.Send(reply); err != nil {
				return err
			}
		}
	}
}

func (leader *scriptedLeader) got(kind string) int {
	leader.mu.Lock()
	defer leader.mu.Unlock()
	total := 0
	for _, message := range leader.received {
		switch kind {
		case "snapshot_request":
			if message.GetSnapshotRequest() != nil {
				total++
			}
		case "receipt_failed":
			if receipt := message.GetReceipt(); receipt != nil && receipt.Failure != "" {
				total++
			}
		case "receipt_installed":
			if receipt := message.GetReceipt(); receipt != nil && receipt.Installed {
				total++
			}
		case "heartbeat":
			if message.GetHeartbeat() != nil {
				total++
			}
		}
	}
	return total
}

// A delta whose base is not what the Worker holds is not applied; the
// Worker reports the failure, asks for a snapshot, and installs the
// snapshot it gets. A tampered snapshot is refused whole and asked again.
// A Leader that refuses the stream sends the Worker back to discovery,
// which can name another Leader; the Worker connects there and installs.
func TestTheWorkerRefusesWhatDoesNotVerifyAndFollowsTheLeaderItIsSentTo(t *testing.T) {
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	v2 := viewFrom(desired, "w1", 2)
	v3 := viewFrom(desired, "w1", 3)
	// A delta from revision 1 to 2 for a Worker that holds nothing, then --
	// on request -- the snapshot of revision 2; then a delta 2 -> 3 with a
	// forged target digest, then the good snapshot of 3.
	badBase := viewstream.DeltaToWire(viewstream.Delta{WorkerID: "w1", Base: viewstream.Version{ControlEpoch: 7, Revision: 1, Digest: "x"}, Target: v2.Version, Publication: publicationA})
	forged := viewstream.DeltaToWire(viewstream.Delta{WorkerID: "w1", Base: v2.Version, Target: viewstream.Version{ControlEpoch: 7, Revision: 3, Digest: "forged"}, Publication: publicationA})
	requests := 0
	scripted := &scriptedLeader{onHello: []*pb.LeaderMessage{{Body: &pb.LeaderMessage_Delta{Delta: badBase}}}}
	scripted.replies = func(message *pb.WorkerMessage) []*pb.LeaderMessage {
		if message.GetSnapshotRequest() == nil {
			return nil
		}
		requests++
		switch requests {
		case 1:
			out := []*pb.LeaderMessage{}
			for _, chunk := range viewstream.SnapshotChunks(v2, 0) {
				out = append(out, &pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}})
			}
			return append(out, &pb.LeaderMessage{Body: &pb.LeaderMessage_Delta{Delta: forged}})
		case 2:
			out := []*pb.LeaderMessage{}
			for _, chunk := range viewstream.SnapshotChunks(v3, 0) {
				out = append(out, &pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}})
			}
			return out
		}
		return nil
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "scripted", scripted)
	discovery := &scriptedDiscovery{}
	discovery.set("scripted", true)
	observer := &sessionObserver{}
	client, _ := startClient(t, discovery, nil, dialer, observer)
	eventually(t, "revision 3 installed after two snapshot requests", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 3
	})
	stats := client.Stats()
	if stats.SnapshotsRequested != 2 || stats.InstallFailures[viewstream.FailureDeltaBaseMismatch] != 1 || stats.InstallFailures[viewstream.FailureDeltaDigest] != 1 || stats.Installs["snapshot"] != 2 {
		t.Fatalf("client stats = %+v, want two snapshot requests, one base and one digest failure, two installs", stats)
	}
	if scripted.got("snapshot_request") != 2 || scripted.got("receipt_failed") != 2 || scripted.got("receipt_installed") != 2 {
		t.Fatalf("leader received: requests=%d failed receipts=%d installed receipts=%d", scripted.got("snapshot_request"), scripted.got("receipt_failed"), scripted.got("receipt_installed"))
	}

	// The Leader steps down: refused NOT_LEADER, the Worker goes back to
	// discovery, which now names a real Leader; the Worker installs there.
	harness := startServer(t)
	if err := harness.server.Lead(8); err != nil {
		t.Fatal(err)
	}
	newTerm := desired
	newTerm.ControlEpoch = 8
	if _, err := harness.server.Publish(context.Background(), newTerm); err != nil {
		t.Fatal(err)
	}
	dialer.serveOn(t, "leader-b", harness.server)
	refusing := &scriptedLeader{onHello: []*pb.LeaderMessage{{Body: &pb.LeaderMessage_Refusal{Refusal: &pb.Refusal{Reason: viewstream.RefusalNotLeader}}}}}
	// The scripted Leader is replaced by one that refuses: the open stream
	// ends with it, and the next connect meets the refusal.
	dialer.serveOn(t, "scripted", refusing)
	eventually(t, "the refusal is counted", func() bool { return client.Stats().Refusals[viewstream.RefusalNotLeader] >= 1 })
	discovery.set("leader-b", true)
	eventually(t, "the Worker installs from the new Leader", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.ControlEpoch == 8 && view.Version.Revision == 1
	})
	if stats := client.Stats(); stats.Leader.Endpoint != "leader-b" || !stats.Connected {
		t.Fatalf("client stats after the move = %+v", stats)
	}
	if observer.count("disconnected", viewstream.RefusalNotLeader) < 1 {
		t.Fatalf("events = %+v, want a NOT_LEADER disconnect", observer.events)
	}
}

// Discovery that finds no Leader, or fails, costs a miss and a wait and
// nothing else; the Worker keeps trying and connects once a Leader appears.
func TestTheWorkerWaitsOutDiscoveryAndConnectsWhenALeaderAppears(t *testing.T) {
	harness := startServer(t)
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.server.Publish(context.Background(), desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})); err != nil {
		t.Fatal(err)
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader-a", harness.server)
	discovery := &scriptedDiscovery{}
	observer := &sessionObserver{}
	client, _ := startClient(t, discovery, nil, dialer, observer)
	eventually(t, "discovery is asked more than once", func() bool { return client.Stats().DiscoveryMisses[viewstream.MissNoLeader] >= 3 })
	if _, ok := client.Installed(); ok {
		t.Fatal("installed a view without a Leader")
	}
	discovery.mu.Lock()
	discovery.err = errors.New("redis down")
	discovery.mu.Unlock()
	eventually(t, "a failing discovery is a miss too", func() bool { return observer.count("discovery_missed", "DISCOVERY_FAILED") >= 1 })
	// The word the discovery names its miss with reaches the reader as is:
	// a Leader with nothing to dial is not "no leader", and a client that
	// folded the two would hide the first behind the second again (#216).
	discovery.mu.Lock()
	discovery.err = nil
	discovery.miss = viewstream.MissLeaderNoEndpoint
	discovery.mu.Unlock()
	eventually(t, "a Leader without an endpoint keeps its own word", func() bool {
		return observer.count("discovery_missed", viewstream.MissLeaderNoEndpoint) >= 2 &&
			client.Stats().DiscoveryMisses[viewstream.MissLeaderNoEndpoint] >= 2
	})
	discovery.mu.Lock()
	discovery.miss = ""
	discovery.mu.Unlock()
	discovery.mu.Lock()
	discovery.err = nil
	discovery.mu.Unlock()
	discovery.set("leader-a", true)
	eventually(t, "the Worker installs once a Leader appears", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 1
	})
}

// A Leader that answers nothing -- no heartbeat reply, no publication --
// for the idle bound loses the stream: the Worker cuts it as LEADER_SILENT,
// goes back to discovery and connects again. Nothing installed is lost.
func TestTheWorkerCutsAStreamOnWhichTheLeaderAnswersNothing(t *testing.T) {
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	snapshot := viewFrom(desired, "w1", 1)
	// The snapshot is answered to the first Hello only: after the cut the
	// Leader sends nothing, so the view the Worker holds afterwards is the
	// one it kept, not one it was given again.
	mute := &scriptedLeader{}
	served := false
	mute.replies = func(message *pb.WorkerMessage) []*pb.LeaderMessage {
		if message.GetHello() == nil || served {
			return nil
		}
		served = true
		var out []*pb.LeaderMessage
		for _, chunk := range viewstream.SnapshotChunks(snapshot, 0) {
			out = append(out, &pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}})
		}
		return out
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "mute", mute)
	discovery := &scriptedDiscovery{}
	discovery.set("mute", true)
	observer := &sessionObserver{}
	clock := &atomic.Int64{}
	clock.Store(time.Unix(1000, 0).UnixMilli())
	client, _ := startClientWithClock(t, discovery, nil, dialer, observer, clock)
	eventually(t, "the snapshot is installed", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 1
	})
	// Heartbeats go out and nothing comes back; then the clock passes the
	// bound. The heartbeat is sent on the tick, so at least one goes out
	// before the silence is judged, as it would on a real clock.
	eventually(t, "a heartbeat is sent", func() bool { return mute.got("heartbeat") >= 1 })
	clock.Add((viewstream.IdleTimeout + time.Second).Milliseconds())
	eventually(t, "the silent Leader's stream is cut", func() bool {
		return observer.count("disconnected", viewstream.DisconnectLeaderSilent) >= 1
	})
	eventually(t, "the Worker connects again", func() bool { return client.Stats().Connections >= 2 })
	// Kept, not re-given: the second Hello was answered with nothing, so a
	// view still here is the one that survived the cut. Losing it on a cut
	// would, once the hot path reads the view, turn every Query Group into
	// "no content" at the moment the control plane is least healthy.
	if view, ok := client.Installed(); !ok || view.Version.Revision != 1 || client.Stats().Installs["snapshot"] != 1 {
		t.Fatalf("the installed view was lost across the cut: %+v ok=%t installs=%v", view, ok, client.Stats().Installs)
	}
	if mute.got("heartbeat") == 0 {
		t.Fatal("no heartbeat was sent before the cut; the silence rule would then be about a stream that never spoke")
	}
}

// lastReceipt is the newest receipt the scripted Leader received.
func (leader *scriptedLeader) lastReceipt() *pb.Receipt {
	leader.mu.Lock()
	defer leader.mu.Unlock()
	for index := len(leader.received) - 1; index >= 0; index-- {
		if receipt := leader.received[index].GetReceipt(); receipt != nil {
			return receipt
		}
	}
	return nil
}

// Between publications the installed view is probed again on every
// heartbeat: an object that goes missing with no install -- expired,
// evicted -- is found, the Worker's count moves, and the Leader is told
// with a receipt for the same version; a heartbeat that finds nothing
// changed tells the Leader nothing.
func TestTheWorkerReprobesTheInstalledViewOnTheHeartbeat(t *testing.T) {
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	snapshot := viewFrom(desired, "w1", 1)
	leader := &scriptedLeader{}
	for _, chunk := range viewstream.SnapshotChunks(snapshot, 0) {
		leader.onHello = append(leader.onHello, &pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}})
	}
	// The Leader answers heartbeats, so the stream is never cut as silent.
	leader.replies = func(message *pb.WorkerMessage) []*pb.LeaderMessage {
		if message.GetHeartbeat() == nil {
			return nil
		}
		return []*pb.LeaderMessage{{Body: &pb.LeaderMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}}}
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader", leader)
	discovery := &scriptedDiscovery{}
	discovery.set("leader", true)
	probe := &countingProbe{missing: map[string]struct{}{}}
	observer := &sessionObserver{}
	clock := &atomic.Int64{}
	clock.Store(time.Unix(1000, 0).UnixMilli())
	client, _ := startClientWithClock(t, discovery, probe, dialer, observer, clock)
	eventually(t, "the snapshot is installed", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 1
	})
	eventually(t, "heartbeats and re-probes run", func() bool { return leader.got("heartbeat") >= 3 })
	if receipts := leader.got("receipt_installed"); receipts != 1 {
		t.Fatalf("receipts while nothing changed = %d, want the install's one", receipts)
	}
	if stats := client.Stats(); stats.ObjectsMissing != 0 || !stats.ObjectsProbed {
		t.Fatalf("stats with everything present = %+v", stats)
	}
	// obj-2 expires from the catalog with no publication in between.
	probe.mu.Lock()
	probe.missing["obj-2"] = struct{}{}
	probe.mu.Unlock()
	eventually(t, "the re-probe finds it and tells the Leader", func() bool {
		receipt := leader.lastReceipt()
		return client.Stats().ObjectsMissing == 1 && receipt != nil && receipt.Installed && receipt.ObjectsMissing == 1 && receipt.ObjectsProbed && receipt.Version.Revision == 1
	})
	if receipts := leader.got("receipt_installed"); receipts != 2 {
		t.Fatalf("receipts after the change = %d, want the install's and one for the change", receipts)
	}
	if observer.count("objects_reprobed", "") != 1 {
		t.Fatalf("events = %+v, want one objects_reprobed", observer.events)
	}
	// The object comes back: one more receipt, then quiet again.
	probe.mu.Lock()
	delete(probe.missing, "obj-2")
	probe.mu.Unlock()
	eventually(t, "the return is reported", func() bool { return leader.got("receipt_installed") == 3 && client.Stats().ObjectsMissing == 0 })
	eventually(t, "more heartbeats pass", func() bool { return leader.got("heartbeat") >= 8 })
	if receipts := leader.got("receipt_installed"); receipts != 3 {
		t.Fatalf("receipts after quiet heartbeats = %d, want still 3", receipts)
	}

	// A new version is installed while a re-probe of the old one is under
	// way: the re-probe's answer is for a version the Worker no longer
	// holds, and no receipt goes out for it -- it would only land in the
	// Leader's unknown_version count and read like a fault. The new version
	// replaces qg-2's object with one the catalog has, so the two answers
	// differ: the old view has one missing, the new none.
	next := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2b", "s2")})
	before, after := snapshot, viewFrom(next, "w1", 2)
	delta, err := viewstream.Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	hold, entered := make(chan struct{}), make(chan struct{})
	probe.mu.Lock()
	// Only a re-probe of revision 1 asks about obj-2; revision 2's install
	// asks about obj-2b and must not be the call that is held.
	probe.hold, probe.entered, probe.holdFor = hold, entered, "obj-2"
	probe.missing["obj-2"] = struct{}{}
	probe.mu.Unlock()
	// The next heartbeat's re-probe blocks; the Leader answers that
	// heartbeat with the delta, which the Worker installs meanwhile.
	deltaSent := false
	leader.mu.Lock()
	leader.replies = func(message *pb.WorkerMessage) []*pb.LeaderMessage {
		if message.GetHeartbeat() == nil {
			return nil
		}
		out := []*pb.LeaderMessage{{Body: &pb.LeaderMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}}}
		if !deltaSent {
			deltaSent = true
			out = append(out, &pb.LeaderMessage{Body: &pb.LeaderMessage_Delta{Delta: viewstream.DeltaToWire(delta)}})
		}
		return out
	}
	leader.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(hold)
		t.Fatal("no heartbeat re-probed revision 1 within five seconds")
	}
	eventually(t, "revision 2 is installed while the re-probe of 1 is held", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision == 2
	})
	close(hold)
	eventually(t, "more heartbeats pass after the release", func() bool { return leader.got("heartbeat") >= 14 })
	leader.mu.Lock()
	staleReceipts := 0
	for _, message := range leader.received {
		if receipt := message.GetReceipt(); receipt != nil && receipt.Version.Revision == 1 && receipt.ObjectsMissing == 1 && receipt.Installed && len(leader.received) > 0 {
			staleReceipts++
		}
	}
	leader.mu.Unlock()
	// One receipt for revision 1 named one missing object: the one sent when
	// obj-2 first went missing. None came from the held re-probe.
	if staleReceipts != 1 {
		t.Fatalf("receipts for revision 1 with one object missing = %d, want only the earlier one; the held re-probe must not report a version the Worker no longer holds", staleReceipts)
	}
	if stats := client.Stats(); stats.Installed.Revision != 2 || stats.ObjectsMissing != 0 || !stats.ObjectsProbed {
		t.Fatalf("stats after the race = %+v, want revision 2 with nothing missing, as its own install found; the stale probe must not overwrite it", stats)
	}
}

// The reconnect wait is full jitter under an exponential ceiling: never
// above the ceiling for the attempt, never above the maximum, and not the
// same every time.
func TestTheReconnectWaitIsJitteredUnderAnExponentialCeiling(t *testing.T) {
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1"}, &scriptedDiscovery{}, nil, nil, viewstream.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waits := viewstream.BackoffsForTest(client, 1, 40)
	if len(waits) != 40 {
		t.Fatal("no waits")
	}
	distinct := map[time.Duration]struct{}{}
	for _, wait := range waits {
		if wait < 0 || wait > viewstream.ReconnectMin {
			t.Fatalf("first attempt waited %s, want within [0, %s]", wait, viewstream.ReconnectMin)
		}
		distinct[wait] = struct{}{}
	}
	if len(distinct) < 10 {
		t.Fatalf("forty first-attempt waits took %d distinct values; the jitter is not there", len(distinct))
	}
	for attempt, ceiling := range map[int]time.Duration{2: 2 * time.Second, 5: 16 * time.Second, 6: viewstream.ReconnectMax, 20: viewstream.ReconnectMax} {
		for _, wait := range viewstream.BackoffsForTest(client, attempt, 20) {
			if wait > ceiling {
				t.Fatalf("attempt %d waited %s, over its ceiling %s", attempt, wait, ceiling)
			}
		}
	}
	if wait := viewstream.BackoffsForTest(client, 0, 1)[0]; wait != 0 {
		t.Fatalf("attempt 0 waited %s, want none", wait)
	}
}

// The stream's reason words are the vocabulary's: every constant the client
// and the server put in a line's reason is admitted as itself, and the lines
// carry it as reason_code. The vocabulary cannot import this package, so this
// is where the two lists are held together.
func TestEveryStreamReasonWordIsInTheVocabularyAndOnTheLine(t *testing.T) {
	for _, word := range []string{
		"NO_LEADER", "DISCOVERY_FAILED", "STREAM_CLOSED", "RECV_FAILED", viewstream.DisconnectLeaderSilent,
		viewstream.FailureDeltaBaseMismatch, viewstream.FailureDeltaDigest, viewstream.FailureSnapshotInvalid,
		viewstream.FailureSnapshotIncomplete, viewstream.FailureWrongWorker,
		viewstream.RefusalNotLeader, viewstream.RefusalUnknownWorker, viewstream.RefusalBadToken, viewstream.RefusalProtocolVersion,
		viewstream.RefusalRegistryUnavailable, viewstream.RefusalHelloExpected, viewstream.RefusalReplaced, viewstream.RefusalIdle, viewstream.RefusalShutdown,
	} {
		if observability.ViewStreamReasonCode(word) != observability.ReasonCode(word) {
			t.Errorf("%s is a stream reason the vocabulary does not admit: the line would say reason_not_reported", word)
		}
	}
	// And a discovery miss reaches the observer with the word as its code.
	discovery := &scriptedDiscovery{}
	observer := &sessionObserver{}
	startClient(t, discovery, nil, &bufconnDialer{}, observer)
	eventually(t, "a discovery miss is observed", func() bool { return observer.codes("discovery_missed")["NO_LEADER"] >= 1 })
	if codes := observer.codes("discovery_missed"); codes[string(observability.ReasonNotReported)] > 0 || codes[""] > 0 {
		t.Errorf("discovery misses observed without the word as reason_code: %v", codes)
	}
}

type recordingCostSink struct {
	mu    sync.Mutex
	costs map[string][]viewstream.QueryGroupCost
}

func (sink *recordingCostSink) RecordCosts(workerID string, costs []viewstream.QueryGroupCost) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.costs == nil {
		sink.costs = map[string][]viewstream.QueryGroupCost{}
	}
	sink.costs[workerID] = append([]viewstream.QueryGroupCost(nil), costs...)
}

type scriptedCostSource struct {
	costs    []viewstream.QueryGroupCost
	sessions *atomic.Int32
}

func (source scriptedCostSource) Costs() []viewstream.QueryGroupCost { return source.costs }
func (source scriptedCostSource) SessionStarted() {
	if source.sessions != nil {
		source.sessions.Add(1)
	}
}

// What the Worker's cost source says rides on its heartbeat and reaches the
// Leader's sink under the Worker's name. The two ends are interfaces the
// producer and the consumer implement on their own sides; this pins the
// wire between them, so neither can be built against a heartbeat that does
// not carry what the other expects.
func TestTheHeartbeatCarriesTheWorkersCostsToTheLeadersSink(t *testing.T) {
	sink := &recordingCostSink{}
	admit := &tokenAdmission{tokens: map[string]string{"w1": "t1"}}
	server, err := viewstream.NewServer(admit, &sessionObserver{}, viewstream.ServerOptions{Tick: 20 * time.Millisecond, Costs: sink})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	if err := server.Lead(7); err != nil {
		t.Fatal(err)
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader-a", server)
	discovery := &scriptedDiscovery{}
	discovery.set("leader-a", true)
	costs := []viewstream.QueryGroupCost{{QueryGroup: "qg-1", RetainedBytesPeak: 175 << 20, CostPerSecondMilli: 570}}
	var sessions atomic.Int32
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1", StreamToken: "t1"}, discovery, nil, &sessionObserver{},
		viewstream.ClientOptions{Dial: dialer.dial, Tick: 20 * time.Millisecond, Costs: scriptedCostSource{costs: costs, sessions: &sessions},
			Sleep: func(ctx context.Context, wait time.Duration) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
					return nil
				}
			}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "the Leader's sink holds w1's costs", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		got := sink.costs["w1"]
		return len(got) == 1 && got[0] == costs[0]
	})
	// The source was told the stream began, once, before the first
	// heartbeat rode on it: what it reports from here is to this Leader,
	// whatever it reported to the last one.
	if sessions.Load() != 1 {
		t.Fatalf("the cost source was told of %d stream starts, want one for the one stream", sessions.Load())
	}
}

// scriptedSwitched answers like the Worker's gate: executable is a set of
// Query Groups, and the count is of those among the entries asked about.
type scriptedSwitched struct {
	mu         sync.Mutex
	executable map[execution.QueryGroupIdentity]struct{}
	// overcount makes the source answer more than it was asked about, the
	// way a source counting the wrong population would.
	overcount bool
}

func (source *scriptedSwitched) set(groups ...execution.QueryGroupIdentity) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.executable = map[execution.QueryGroupIdentity]struct{}{}
	for _, group := range groups {
		source.executable[group] = struct{}{}
	}
}

func (source *scriptedSwitched) SwitchedQueryGroups(of []execution.QueryGroupIdentity) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.overcount {
		return len(of) + 1
	}
	count := 0
	for _, group := range of {
		if _, ok := source.executable[group]; ok {
			count++
		}
	}
	return count
}

// The receipt says how many of the view's Query Groups the Worker executes
// from it and claims switched only when that is all of them; the count moves
// between heartbeats without a new version, and the Leader can read how far
// short of switched a Worker is.
func TestTheReceiptCountsTheQueryGroupsExecutedFromTheViewAndClaimsSwitchedOnlyForAll(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	first := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	if _, err := harness.server.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader-a", harness.server)
	discovery := &scriptedDiscovery{}
	discovery.set("leader-a", true)
	switched := &scriptedSwitched{}
	switched.set("qg-1")
	clock := &atomic.Int64{}
	clock.Store(time.Unix(1000, 0).UnixMilli())
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1", StreamToken: "t1"}, discovery, nil, &sessionObserver{},
		viewstream.ClientOptions{Dial: dialer.dial, Tick: 20 * time.Millisecond, Switched: switched,
			Now: func() time.Time { return time.UnixMilli(clock.Load()) },
			Sleep: func(ctx context.Context, wait time.Duration) error {
				time.Sleep(5 * time.Millisecond)
				return ctx.Err()
			}})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "one of two executed from the view, not switched", func() bool {
		stats := harness.server.Stats()
		lagging := harness.server.Stats().NotSwitched
		return stats.Counts.Installed == 1 && stats.Counts.Switched == 0 &&
			len(lagging) == 1 && lagging[0].WorkerID == "w1" && lagging[0].SwitchedQueryGroups == 1
	})
	// The second Query Group's checks come good between heartbeats: the next
	// receipt claims switched with no new version.
	switched.set("qg-1", "qg-2")
	eventually(t, "both executed from the view, switched", func() bool {
		stats := harness.server.Stats()
		return stats.Counts.Switched == 1 && stats.Revision == 1 && client.Stats().SwitchedQueryGroups == 2
	})

	// A delta moves qg-2 away. The Worker still runs it until its next read
	// and the gate still calls it executable; the new version names one
	// entry, and only that entry counts - a version is switched by its own
	// entries, not by everything the Worker runs. qg-1 alone is 1 of 1:
	// switched; had qg-2 counted, 2 of 1 would have read as switched too,
	// for the wrong reason, so the gate is made to lose qg-1 as well.
	switched.set("qg-2")
	second := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	if _, err := harness.server.Publish(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the moved-away Query Group does not count for the new version", func() bool {
		stats := harness.server.Stats()
		return stats.Revision == 2 && stats.Counts.Installed == 1 && stats.Counts.Switched == 0 &&
			len(stats.NotSwitched) == 1 && stats.NotSwitched[0].SwitchedQueryGroups == 0
	})

	// A source that answers more than the version has is counting the
	// wrong population; that is not "all of them", it is nothing.
	switched.mu.Lock()
	switched.overcount = true
	switched.mu.Unlock()
	third := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-3": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-3": content("obj-3", "s3")})
	if _, err := harness.server.Publish(ctx, third); err != nil {
		t.Fatal(err)
	}
	eventually(t, "an overcounting source claims nothing", func() bool {
		stats := harness.server.Stats()
		return stats.Revision == 3 && stats.Counts.Installed == 1 && stats.Counts.Switched == 0 &&
			len(stats.NotSwitched) == 1 && stats.NotSwitched[0].SwitchedQueryGroups == 0
	})
}
