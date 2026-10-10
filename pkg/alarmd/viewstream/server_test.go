// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// tokenAdmission admits the Workers it knows with the token each wrote.
type tokenAdmission struct {
	mu     sync.Mutex
	tokens map[string]string
	fail   error
}

func (admission *tokenAdmission) Admit(_ context.Context, workerID, token string) (string, error) {
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if admission.fail != nil {
		return "", admission.fail
	}
	want, known := admission.tokens[workerID]
	if !known {
		return viewstream.RefusalUnknownWorker, nil
	}
	if want != token {
		return viewstream.RefusalBadToken, nil
	}
	return "", nil
}

type sessionObserver struct {
	mu     sync.Mutex
	events []observability.ViewStreamFacts
	// coded is the reason_code each event was observed with, in step with
	// events: the line's bounded word, apart from the facts' free reason.
	coded []observability.ReasonCode
}

func (observer *sessionObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ViewStream == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, *observation.ViewStream)
	observer.coded = append(observer.coded, observation.ReasonCode)
}

// codes counts one event's observations by the reason_code they carried.
func (observer *sessionObserver) codes(event string) map[string]int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	codes := map[string]int{}
	for index, fact := range observer.events {
		if fact.Event == event {
			codes[string(observer.coded[index])]++
		}
	}
	return codes
}

func (observer *sessionObserver) count(event, reason string) int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	total := 0
	for _, fact := range observer.events {
		if fact.Event == event && (reason == "" || fact.Reason == reason) {
			total++
		}
	}
	return total
}

type streamHarness struct {
	t        *testing.T
	server   *viewstream.Server
	grpc     *grpc.Server
	listener *bufconn.Listener
	observer *sessionObserver
	admit    *tokenAdmission
	clock    *atomic.Int64
}

func startServer(t *testing.T) *streamHarness {
	t.Helper()
	admit := &tokenAdmission{tokens: map[string]string{"w1": "t1", "w2": "t2", "w3": "t3"}}
	observer := &sessionObserver{}
	clock := &atomic.Int64{}
	clock.Store(time.Unix(1000, 0).UnixMilli())
	server, err := viewstream.NewServer(admit, observer, viewstream.ServerOptions{
		Now: func() time.Time { return time.UnixMilli(clock.Load()) }, Tick: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pb.RegisterControlServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		grpcServer.Stop()
	})
	return &streamHarness{t: t, server: server, grpc: grpcServer, listener: listener, observer: observer, admit: admit, clock: clock}
}

type workerStream struct {
	t      *testing.T
	stream pb.ControlService_ConnectClient
	cancel context.CancelFunc
}

func (harness *streamHarness) connect(worker, token, incarnation string, installed *pb.Version) *workerStream {
	harness.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return harness.listener.Dial() }))
	if err != nil {
		cancel()
		harness.t.Fatal(err)
	}
	stream, err := pb.NewControlServiceClient(conn).Connect(ctx)
	if err != nil {
		cancel()
		harness.t.Fatal(err)
	}
	harness.t.Cleanup(func() { cancel(); _ = conn.Close() })
	ws := &workerStream{t: harness.t, stream: stream, cancel: cancel}
	if token != "-" {
		ws.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{
			WorkerId: worker, Incarnation: incarnation, ProtocolVersion: viewstream.ProtocolVersion, StreamToken: token, Installed: installed,
		}}})
	}
	return ws
}

func (ws *workerStream) send(message *pb.WorkerMessage) {
	ws.t.Helper()
	if err := ws.stream.Send(message); err != nil {
		ws.t.Fatal(err)
	}
}

// recv returns the next message within a second.
func (ws *workerStream) recv() *pb.LeaderMessage {
	ws.t.Helper()
	type got struct {
		message *pb.LeaderMessage
		err     error
	}
	done := make(chan got, 1)
	go func() {
		message, err := ws.stream.Recv()
		done <- got{message, err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			ws.t.Fatalf("recv: %v", result.err)
		}
		return result.message
	case <-time.After(2 * time.Second):
		ws.t.Fatal("no message within two seconds")
		return nil
	}
}

func (ws *workerStream) recvSnapshot() *pb.Snapshot {
	ws.t.Helper()
	message := ws.recv()
	if message.GetSnapshot() == nil {
		ws.t.Fatalf("want a snapshot, got %s", describe(message))
	}
	return message.GetSnapshot()
}

func (ws *workerStream) recvDelta() *pb.Delta {
	ws.t.Helper()
	message := ws.recv()
	if message.GetDelta() == nil {
		ws.t.Fatalf("want a delta, got %s", describe(message))
	}
	return message.GetDelta()
}

// describe names a message by kind and size. A view of thousands of entries
// printed whole is a failure nobody reads.
func describe(message *pb.LeaderMessage) string {
	if message == nil {
		return "nothing"
	}
	size := proto.Size(message)
	if size > 4096 {
		return fmt.Sprintf("%T of %d bytes", message.Body, size)
	}
	return fmt.Sprintf("%+v", message)
}

func (ws *workerStream) recvRefusal() string {
	ws.t.Helper()
	message := ws.recv()
	if message.GetRefusal() == nil {
		ws.t.Fatalf("want a refusal, got %+v", message)
	}
	return message.GetRefusal().Reason
}

func (ws *workerStream) receipt(incarnation string, version *pb.Version, installed bool) {
	ws.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Receipt{Receipt: &pb.Receipt{
		Incarnation: incarnation, Version: version, Acked: true, Installed: installed,
	}}})
}

// probedReceipt is an installed receipt that carries what the probe found.
func (ws *workerStream) probedReceipt(incarnation string, version *pb.Version, missing uint32) {
	ws.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Receipt{Receipt: &pb.Receipt{
		Incarnation: incarnation, Version: version, Acked: true, Installed: true, ObjectsProbed: true, ObjectsMissing: missing,
	}}})
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within two seconds", what)
}

// A Worker that says Hello gets the current snapshot; a publication that
// moves its projection brings a delta once it has installed the previous
// revision; one that moves another Worker's brings an empty delta; a
// Worker that never installed gets the next snapshot; receipts move the
// ledger the page reads; a SnapshotRequest is answered with the whole view.
func TestTheServerBringsEachWorkerToTheCurrentRevisionBySnapshotOrOneStep(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	first := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w3"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	if _, err := harness.server.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	w2 := harness.connect("w2", "t2", "i2", nil)
	w3 := harness.connect("w3", "t3", "i3", nil)
	snap1 := w1.recvSnapshot()
	snap2 := w2.recvSnapshot()
	_ = w3.recvSnapshot()
	if snap1.Version.Revision != 1 || len(snap1.Entries) != 1 || snap1.Entries[0].QueryGroup != "qg-1" || snap1.Chunks != 1 {
		t.Fatalf("w1 snapshot = %+v", snap1)
	}
	eventually(t, "three sessions and three sent", func() bool {
		stats := harness.server.Stats()
		return stats.Sessions == 3 && stats.Counts.Sent == 3 && stats.Counts.Expected == 3
	})
	// w1 and w2 install; w3 says nothing.
	w1.receipt("i1", snap1.Version, true)
	w2.receipt("i2", snap2.Version, true)
	eventually(t, "two installed", func() bool { return harness.server.Stats().Counts.Installed == 2 })
	stats := harness.server.Stats()
	if len(stats.Lagging) != 1 || stats.Lagging[0].WorkerID != "w3" || !stats.Lagging[0].Connected {
		t.Fatalf("lagging = %+v, want w3, connected", stats.Lagging)
	}

	// Only w1's content moves: w1 gets a real delta, w2 an empty one, w3 --
	// which never installed revision 1 -- a snapshot of revision 2.
	second := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w3"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	published, err := harness.server.Publish(ctx, second)
	if err != nil || !published.Changed || published.Version.Revision != 2 {
		t.Fatalf("publish = %+v (%v)", published, err)
	}
	delta1 := w1.recvDelta()
	if delta1.Base.Revision != 1 || delta1.Target.Revision != 2 || len(delta1.Upserts) != 1 || delta1.Upserts[0].Content.ObjectDigest != "obj-1b" {
		t.Fatalf("w1 delta = %+v", delta1)
	}
	delta2 := w2.recvDelta()
	if len(delta2.Upserts) != 0 || len(delta2.Removed) != 0 || delta2.Base.Digest != delta2.Target.Digest || delta2.Target.Revision != 2 {
		t.Fatalf("w2 delta = %+v, want empty to revision 2", delta2)
	}
	snap3 := w3.recvSnapshot()
	if snap3.Version.Revision != 2 || len(snap3.Entries) != 1 {
		t.Fatalf("w3 snapshot = %+v, want revision 2 whole", snap3)
	}
	stats = harness.server.Stats()
	if stats.Revision != 2 || stats.Counts != (viewstream.Counts{Expected: 3, Sent: 3}) || stats.DeltasSent != 1 || stats.EmptyDeltasSent != 1 || stats.SnapshotChunksSent != 4 {
		t.Fatalf("stats after second publish = %+v", stats)
	}
	// Publishing the same set again moves nothing and sends nothing.
	if again, err := harness.server.Publish(ctx, second); err != nil || again.Changed {
		t.Fatalf("republish = %+v (%v)", again, err)
	}
	if stats := harness.server.Stats(); stats.PublicationsSkipped != 1 || stats.Publications != 2 {
		t.Fatalf("publication counters = %+v", stats)
	}
	// A Worker whose delta did not apply asks for a snapshot and gets one.
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_SnapshotRequest{SnapshotRequest: &pb.SnapshotRequest{Reason: "DELTA_BASE_MISMATCH"}}})
	requested := w1.recvSnapshot()
	if requested.Version.Revision != 2 || len(requested.Entries) != 1 || requested.Entries[0].Content.ObjectDigest != "obj-1b" {
		t.Fatalf("requested snapshot = %+v", requested)
	}
	// A heartbeat is answered with the version the Leader last sent; an
	// object request is answered unavailable in this protocol version.
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{SentAtMs: 1}}})
	if beat := w1.recv().GetHeartbeat(); beat == nil || beat.Installed.Revision != 2 {
		t.Fatalf("heartbeat reply = %+v", beat)
	}
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_ObjectRequest{ObjectRequest: &pb.ObjectRequest{Kind: "query_group", Digest: "obj-1b"}}})
	if result := w1.recv().GetObjectResult(); result == nil || result.Unavailable != viewstream.ObjectUnavailableUnsupported || len(result.Payload) != 0 {
		t.Fatalf("object result = %+v", result)
	}
	// A receipt for a digest that is not this Worker's is counted ignored.
	w1.receipt("i1", &pb.Version{ControlEpoch: 7, Revision: 2, Digest: "forged"}, true)
	eventually(t, "the forged receipt is counted", func() bool { return harness.server.Stats().Ignored.DigestMismatch == 1 })
	if harness.observer.count("published", "") != 2 || harness.observer.count("opened", "") != 3 {
		t.Fatalf("events = %+v", harness.observer.events)
	}
	// w3 installs revision 2 too: the version is installed by all three,
	// and the Leader says so once with the time it took.
	w1.receipt("i1", delta1.Target, true)
	w2.receipt("i2", delta2.Target, true)
	w3.receipt("i3", snap3.Version, true)
	eventually(t, "installed by all is reported once", func() bool { return harness.observer.count("installed_by_all", "") == 1 })
	w3.receipt("i3", snap3.Version, true)
	time.Sleep(20 * time.Millisecond)
	if harness.observer.count("installed_by_all", "") != 1 {
		t.Fatalf("installed_by_all reported %d times, want once", harness.observer.count("installed_by_all", ""))
	}
}

// A Worker that reconnects saying which revision it holds is brought
// forward by one delta when that revision is the previous one, by a
// snapshot when it is older or of another term, and by nothing when it is
// the current one.
func TestAReconnectingWorkerIsBroughtForwardFromWhatItSaysItHolds(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	first := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	if _, err := harness.server.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	v1, _ := harness.server.Stats().Current, 0
	w1 := harness.connect("w1", "t1", "i1", nil)
	snap1 := w1.recvSnapshot()
	if snap1.Version.Revision != v1.Revision {
		t.Fatalf("first snapshot revision %d, want %d", snap1.Version.Revision, v1.Revision)
	}
	w1.cancel()
	second := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1b", "s1")})
	if _, err := harness.server.Publish(ctx, second); err != nil {
		t.Fatal(err)
	}
	// Back with revision 1 in hand: one delta to revision 2.
	again := harness.connect("w1", "t1", "i1b", snap1.Version)
	delta := again.recvDelta()
	if delta.Base.Revision != 1 || delta.Target.Revision != 2 || len(delta.Upserts) != 1 {
		t.Fatalf("reconnect one behind got %+v, want the 1 -> 2 delta", delta)
	}
	again.cancel()
	// The receipt for revision 2 is lost with the stream; the Worker comes
	// back saying it holds revision 2. The ledger credits the claim -- sent
	// and installed, objects unprobed -- so the version is not short a
	// receiver for a Worker that holds it, and installed-by-all is reported
	// for the one expected receiver. Back with revision 2 in hand: nothing
	// until the next publication.
	again.cancel()
	current := harness.connect("w1", "t1", "i1c", delta.Target)
	eventually(t, "the claimed install is credited", func() bool {
		stats := harness.server.Stats()
		return stats.Counts.Installed == 1 && stats.Counts.Sent == 1 && stats.Counts.Expected == 1
	})
	// The same process comes back claiming revision 2 under a digest that is
	// not its view: not credited, counted as a digest mismatch, given a
	// snapshot -- and the record it already holds on revision 2 stays as it
	// was. The claim never reaches the ledger as a receipt would not.
	current.cancel()
	forged := harness.connect("w1", "t1", "i1c", &pb.Version{ControlEpoch: 7, Revision: 2, Digest: "not-mine"})
	_ = forged.recvSnapshot()
	eventually(t, "the forged claim is ignored", func() bool { return harness.server.Stats().Ignored.DigestMismatch == 1 })
	if counts := harness.server.Stats().Counts; counts.Installed != 1 || counts.Sent != 1 {
		t.Fatalf("after a forged claim from the same process the record reads %+v, want installed 1 and sent 1 untouched", counts)
	}
	forged.cancel()
	// A new incarnation of the Worker is a restart, forged claim or not: the
	// old process's stages are void, the new one starts from sent. That is
	// the ledger's rule for a restart, applied at the snapshot it is given,
	// and not something a claim's digest decides.
	restarted := harness.connect("w1", "t1", "i1x", &pb.Version{ControlEpoch: 7, Revision: 2, Digest: "not-mine"})
	_ = restarted.recvSnapshot()
	eventually(t, "the restart voids the old process's install", func() bool {
		counts := harness.server.Stats().Counts
		return counts.Sent == 1 && counts.Installed == 0 && harness.server.Stats().Ignored.DigestMismatch == 2
	})
	restarted.cancel()
	current = harness.connect("w1", "t1", "i1c", delta.Target)
	eventually(t, "the old process's claim is credited again", func() bool { return harness.server.Stats().Counts.Installed == 1 })
	// Two versions were each completed by a claim -- revision 1 by the
	// reconnect that named it, revision 2 by this one -- and each is
	// reported once.
	if harness.observer.count("installed_by_all", "") != 2 {
		t.Fatalf("installed_by_all reported %d times after the claims, want once per version", harness.observer.count("installed_by_all", ""))
	}
	if lagging := harness.server.Stats().Lagging; len(lagging) != 0 {
		t.Fatalf("lagging after the claim = %+v, want nobody", lagging)
	}
	current.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if reply := current.recv(); reply.GetHeartbeat() == nil {
		t.Fatalf("reconnect at the current revision got %+v before any heartbeat reply, want nothing but the heartbeat", reply)
	}
	current.cancel()
	// Back with a version of another term: a snapshot.
	stale := harness.connect("w1", "t1", "i1d", &pb.Version{ControlEpoch: 6, Revision: 2, Digest: delta.Target.Digest})
	if snap := stale.recvSnapshot(); snap.Version.Revision != 2 {
		t.Fatalf("reconnect from another term got %+v, want the current snapshot", snap)
	}
}

// Every way a stream is refused says why: a bad token, an unknown Worker,
// another protocol version, a first message that is not a Hello, a
// registry that cannot be read, and a Leader that is not leading. A
// refused Worker was never a session.
func TestTheServerRefusesInWords(t *testing.T) {
	harness := startServer(t)
	notLeading := harness.connect("w1", "t1", "i1", nil)
	if reason := notLeading.recvRefusal(); reason != viewstream.RefusalNotLeader {
		t.Fatalf("before leading: %s", reason)
	}
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		worker, token string
		want          string
		prepare       func()
	}{
		"bad token":      {worker: "w1", token: "wrong", want: viewstream.RefusalBadToken},
		"unknown worker": {worker: "w9", token: "t9", want: viewstream.RefusalUnknownWorker},
		"registry down": {worker: "w1", token: "t1", want: viewstream.RefusalRegistryUnavailable, prepare: func() {
			harness.admit.mu.Lock()
			harness.admit.fail = errors.New("redis down")
			harness.admit.mu.Unlock()
		}},
	} {
		if test.prepare != nil {
			test.prepare()
		}
		ws := harness.connect(test.worker, test.token, "i", nil)
		if reason := ws.recvRefusal(); reason != test.want {
			t.Fatalf("%s: refusal = %s, want %s", name, reason, test.want)
		}
		harness.admit.mu.Lock()
		harness.admit.fail = nil
		harness.admit.mu.Unlock()
	}
	other := harness.connect("w1", "-", "i1", nil)
	other.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{WorkerId: "w1", Incarnation: "i1", ProtocolVersion: 99, StreamToken: "t1"}}})
	if reason := other.recvRefusal(); reason != viewstream.RefusalProtocolVersion {
		t.Fatalf("protocol version: %s", reason)
	}
	notHello := harness.connect("w1", "-", "i1", nil)
	notHello.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if reason := notHello.recvRefusal(); reason != viewstream.RefusalHelloExpected {
		t.Fatalf("first message not a Hello: %s", reason)
	}
	if stats := harness.server.Stats(); stats.Sessions != 0 || stats.Refusals != 6 {
		t.Fatalf("stats = %+v, want no session and six refusals", stats)
	}
	if harness.observer.count("refused", viewstream.RefusalBadToken) != 1 || harness.observer.count("opened", "") != 0 {
		t.Fatalf("events = %+v", harness.observer.events)
	}
	// Each refusal's line carries its word as reason_code, not a placeholder.
	if codes := harness.observer.codes("refused"); codes[viewstream.RefusalBadToken] != 1 || codes[viewstream.RefusalHelloExpected] != 1 ||
		codes[""] > 0 || codes[string(observability.ReasonNotReported)] > 0 {
		t.Fatalf("refusal reason codes = %v, want each refusal's own word", codes)
	}
}

// A second stream from the same Worker replaces the first, which is told
// so; a Worker that stops heartbeating is closed as idle and drops out of
// the session table while staying in the version's frozen denominator; a
// Leader that steps down tells every session it is not the Leader.
func TestTheServerKeepsOneSessionPerWorkerAndDropsTheSilent(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	if _, err := harness.server.Publish(ctx, desired); err != nil {
		t.Fatal(err)
	}
	first := harness.connect("w1", "t1", "i1", nil)
	_ = first.recvSnapshot()
	second := harness.connect("w1", "t1", "i1b", nil)
	snapSecond := second.recvSnapshot()
	if reason := first.recvRefusal(); reason != viewstream.RefusalReplaced {
		t.Fatalf("first stream: %s", reason)
	}
	eventually(t, "one session for w1", func() bool { return harness.server.Stats().Sessions == 1 })
	// The restart voided the first incarnation's sent: the second is the one
	// counted, and a receipt still naming the first incarnation is stale.
	second.receipt("i1", snapSecond.Version, true)
	eventually(t, "the stale receipt is counted", func() bool { return harness.server.Stats().Ignored.StaleIncarnation == 1 })
	w2 := harness.connect("w2", "t2", "i2", nil)
	snap2 := w2.recvSnapshot()
	eventually(t, "two sent", func() bool { return harness.server.Stats().Counts.Sent == 2 })

	// w2 goes silent past the idle bound while w1 keeps heartbeating: w2 is
	// closed IDLE and out of the table, still expected by the version.
	harness.clock.Add((viewstream.IdleTimeout / 2).Milliseconds())
	second.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if second.recv().GetHeartbeat() == nil {
		t.Fatal("heartbeat not answered")
	}
	harness.clock.Add((viewstream.IdleTimeout/2 + time.Second).Milliseconds())
	if reason := w2.recvRefusal(); reason != viewstream.RefusalIdle {
		t.Fatalf("idle worker: %s", reason)
	}
	eventually(t, "w2 dropped", func() bool {
		stats := harness.server.Stats()
		return stats.Sessions == 1 && stats.Counts.Expected == 2
	})
	stats := harness.server.Stats()
	if len(stats.Lagging) != 2 {
		t.Fatalf("lagging = %+v", stats.Lagging)
	}
	for _, lagging := range stats.Lagging {
		if lagging.WorkerID == "w2" && lagging.Connected {
			t.Fatalf("w2 reads connected after being dropped: %+v", lagging)
		}
	}
	_ = snap2
	// The second w1 stream heartbeats and stays.
	second.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if second.recv().GetHeartbeat() == nil {
		t.Fatal("heartbeat not answered")
	}
	// Stepping down refuses what is left.
	harness.server.StepDown()
	if reason := second.recvRefusal(); reason != viewstream.RefusalNotLeader {
		t.Fatalf("after step down: %s", reason)
	}
	eventually(t, "no sessions", func() bool { return harness.server.Stats().Sessions == 0 })
	if _, err := harness.server.Publish(ctx, desired); err == nil {
		t.Fatal("publishing while not leading must fail")
	}
	if harness.observer.count("closed", viewstream.RefusalIdle) != 1 || harness.observer.count("closed", viewstream.RefusalReplaced) != 1 {
		t.Fatalf("events = %+v", harness.observer.events)
	}
}

// A Worker's claim at its Hello says nothing about its objects. The Worker
// reports a probe that found three missing, loses its stream, and comes
// back claiming the version it holds: the claim credits the install and
// leaves the probe's count where it was. Read as a receipt -- which is what
// the same claim looks like on the wire, an installed receipt with the
// probe flag unset -- it would have turned the count into "could not
// probe" on every reconnect, until the next heartbeat's probe put it back.
func TestAHelloClaimLeavesTheProbedCountWhereItWas(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, map[string]string{"qg-1": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	snapshot := w1.recvSnapshot()
	w1.probedReceipt("i1", snapshot.Version, 3)
	eventually(t, "the probed receipt is on the account", func() bool {
		objects := harness.server.Stats().Objects
		return objects.Probed == 1 && objects.Missing == 3
	})
	w1.cancel()
	again := harness.connect("w1", "t1", "i1", snapshot.Version)
	eventually(t, "the claim is credited", func() bool { return harness.server.Stats().Counts.Installed == 1 })
	if objects := harness.server.Stats().Objects; objects.Probed != 1 || objects.Missing != 3 || objects.Unprobed != 0 {
		t.Fatalf("objects after the Hello claim = %+v, want the probe's 3 missing over one probed Worker left as it was", objects)
	}
	// The same Worker's next receipt says its probe failed: that is the
	// Worker's word on its objects and it stands.
	again.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Receipt{Receipt: &pb.Receipt{
		Incarnation: "i1", Version: snapshot.Version, Acked: true, Installed: true, ObjectsProbed: false}}})
	eventually(t, "the failed probe is on the account", func() bool {
		objects := harness.server.Stats().Objects
		return objects.Probed == 0 && objects.Unprobed == 1 && objects.Missing == 0
	})
	again.cancel()
}

// A step that adds more than one message's worth of view is not sent as a
// delta: the receiver would refuse the message, and on reconnect the same
// delta would be built again. The Worker gets the chunked snapshot of the
// same revision instead, and the page counts the delta it did not get. A
// small step to the same Worker is still one delta, so the bound and not
// the stage decides.
func TestAStepLargerThanOneMessageIsSentAsAChunkedSnapshot(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	assign := map[string]string{"qg-0": "w1"}
	published := map[string]viewstream.Content{"qg-0": content("obj-0", "s0")}
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, assign, published)); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	snap := w1.recvSnapshot()
	w1.receipt("i1", snap.Version, true)
	eventually(t, "installed", func() bool { return harness.server.Stats().Counts.Installed == 1 })

	// One more Query Group: a step, and a small one.
	assign["qg-1"], published["qg-1"] = "w1", content("obj-1", "s1")
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, assign, published)); err != nil {
		t.Fatal(err)
	}
	small := w1.recvDelta()
	if small.Target.Revision != 2 || len(small.Upserts) != 1 {
		t.Fatalf("small step = %+v, want one upsert at revision 2", small)
	}
	w1.receipt("i1", small.Target, true)
	eventually(t, "installed revision 2", func() bool {
		return harness.server.Stats().Revision == 2 && harness.server.Stats().Counts.Installed == 1
	})

	// grow adds Query Groups to the Worker until their wire size passes
	// bytes. The size is measured from the entries as they would go on the
	// wire, not guessed from a count, so the two steps below land on the
	// sides of the bound they are meant to.
	grow := func(prefix string, bytes int) {
		for index, wireBytes := 0, 0; wireBytes < bytes; index++ {
			name := prefix + strconv.Itoa(index)
			assign[name], published[name] = "w1", content("obj-"+name, "s-"+name)
			one, err := desiredAt(publicationA, map[string]string{name: "w1"}, map[string]viewstream.Content{name: published[name]}).Project("w1")
			if err != nil {
				t.Fatal(err)
			}
			wireBytes += proto.Size(viewstream.SnapshotChunks(one, 0)[0].Entries[0])
		}
	}

	// A step well past half a message is still one delta: the bound, not
	// the size of the step, decides.
	grow("qg-medium-", 3*viewstream.MessageBytes/4)
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, assign, published)); err != nil {
		t.Fatal(err)
	}
	medium := w1.recvDelta()
	if size := proto.Size(medium); medium.Target.Revision != 3 || size < viewstream.MessageBytes/2 || size > viewstream.MessageBytes {
		t.Fatalf("medium step: revision %d, %d bytes; the fixture meant a delta between half a message and one", medium.Target.Revision, size)
	}
	w1.receipt("i1", medium.Target, true)
	eventually(t, "installed revision 3", func() bool {
		return harness.server.Stats().Revision == 3 && harness.server.Stats().Counts.Installed == 1
	})

	// A step larger than a message is not a delta: the Worker gets the
	// snapshot of the same revision, in chunks.
	grow("qg-large-", 2*viewstream.MessageBytes)
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, assign, published)); err != nil {
		t.Fatal(err)
	}
	first := w1.recvSnapshot()
	if first.Version.Revision != 4 || first.Chunks < 3 {
		t.Fatalf("large step came as %d chunk(s) of revision %d, want a chunked snapshot of revision 4", first.Chunks, first.Version.Revision)
	}
	for chunk := uint32(2); chunk <= first.Chunks; chunk++ {
		if next := w1.recvSnapshot(); next.Chunk != chunk {
			t.Fatalf("chunk %d arrived as %d", chunk, next.Chunk)
		}
	}
	stats := harness.server.Stats()
	if stats.DeltasSent != 2 || stats.DeltasOversized != 1 || stats.SnapshotChunksSent != 1+uint64(first.Chunks) {
		t.Fatalf("stats = %+v, want two deltas sent, one oversized, and the first snapshot plus %d chunks", stats, first.Chunks)
	}

	// The snapshot moved the session on: once the Worker installs it, the
	// next small step is one delta from revision 4, not a snapshot again.
	// A session that replaced the delta but did not record the snapshot as
	// sent would answer this step with the snapshot a second time.
	w1.receipt("i1", first.Version, true)
	eventually(t, "installed revision 4", func() bool {
		return harness.server.Stats().Revision == 4 && harness.server.Stats().Counts.Installed == 1
	})
	assign["qg-after"], published["qg-after"] = "w1", content("obj-after", "s-after")
	if _, err := harness.server.Publish(ctx, desiredAt(publicationA, assign, published)); err != nil {
		t.Fatal(err)
	}
	after := w1.recvDelta()
	if after.Base.Revision != 4 || after.Target.Revision != 5 || len(after.Upserts) != 1 {
		t.Fatalf("step after the snapshot = base %d target %d with %d upserts, want one delta 4 -> 5",
			after.Base.Revision, after.Target.Revision, len(after.Upserts))
	}
}
