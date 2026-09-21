// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// The Leader's side of the stream. One session per connected Worker; a
// second stream from the same Worker replaces the first. A session sends
// the Worker its view whenever the term's revision moves past what it last
// sent: the one-step delta when the Worker has installed exactly the
// previous revision, the whole snapshot otherwise -- so a slow Worker is
// brought to the latest snapshot and never handed a queue of deltas.
// Receipts go to the term's ledger. Nothing here touches execution: a Leader
// that cannot serve a Worker refuses it in words and the Worker keeps
// executing off what it reads today.

// Refusal reasons, the bounded words of pb.Refusal.
const (
	RefusalNotLeader           = "NOT_LEADER"
	RefusalUnknownWorker       = "UNKNOWN_WORKER"
	RefusalBadToken            = "BAD_TOKEN"
	RefusalProtocolVersion     = "PROTOCOL_VERSION"
	RefusalRegistryUnavailable = "REGISTRY_UNAVAILABLE"
	RefusalHelloExpected       = "HELLO_EXPECTED"
	RefusalReplaced            = "REPLACED_BY_NEW_STREAM"
	RefusalIdle                = "IDLE"
	RefusalShutdown            = "SHUTDOWN"
)

// Timing constants of the stream. Not configuration: they are the
// protocol's own pace, the same on every deployment.
const (
	// HelloTimeout is how long a new stream has to say Hello.
	HelloTimeout = 10 * time.Second
	// HeartbeatInterval is how often a Worker heartbeats; a session that
	// hears nothing for IdleTimeout is closed.
	HeartbeatInterval = 10 * time.Second
	IdleTimeout       = 3 * HeartbeatInterval
	// ObjectUnavailableUnsupported answers an ObjectRequest in this version
	// of the protocol: objects are read through the catalog, not fetched
	// here.
	ObjectUnavailableUnsupported = "OBJECT_REQUEST_UNSUPPORTED"
)

// Admission decides whether a Hello may open a stream. It answers "" to
// admit, or the refusal reason; an error is the registry being unreadable,
// which refuses with RefusalRegistryUnavailable. The token is compared
// against the one the Worker wrote into its own registration, so the
// registry is the trust root and nothing new is distributed.
type Admission interface {
	Admit(ctx context.Context, workerID, streamToken string) (string, error)
}

// Stats is the Leader's view of the stream for the page and the metrics.
type Stats struct {
	Leading      bool
	ControlEpoch uint64
	Revision     uint64
	Sessions     int
	// Current is the four numbers of the current version; Key its identity;
	// Objects what its installed receivers said about their objects.
	Current Key
	Counts  Counts
	Objects ObjectsSummary
	Ignored Ignored
	// Lagging lists the Workers that have not installed the current version.
	Lagging []LaggingReceiver
	// Counters since the process started.
	Publications        uint64
	PublicationsSkipped uint64
	SnapshotChunksSent  uint64
	DeltasSent          uint64
	EmptyDeltasSent     uint64
	Refusals            uint64
}

type serverCounters struct {
	publications, publicationsSkipped, snapshotChunks, deltas, emptyDeltas, refusals uint64
}

// Server implements pb.ControlServiceServer for the Leader.
type Server struct {
	pb.UnimplementedControlServiceServer
	admission Admission
	observer  observability.Observer
	now       func() time.Time
	// tick is how often a session checks for idleness; HeartbeatInterval in
	// production, shorter in a test that drives the clock.
	tick time.Duration

	mu        sync.Mutex
	publisher *Publisher
	sessions  map[string]*session
	counters  serverCounters
	closed    bool
}

// ServerOptions are the seams a test needs: a clock, and how often the
// idle check runs against it. Production leaves both zero.
type ServerOptions struct {
	Now  func() time.Time
	Tick time.Duration
}

func NewServer(admission Admission, observer observability.Observer, options ServerOptions) (*Server, error) {
	if admission == nil {
		return nil, errors.New("alarmd viewstream: server needs an admission")
	}
	if observer == nil {
		observer = observability.ObserverFunc(func(context.Context, observability.Observation) {})
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	tick := options.Tick
	if tick <= 0 {
		tick = HeartbeatInterval
	}
	return &Server{admission: admission, observer: observer, now: now, tick: tick, sessions: map[string]*session{}}, nil
}

// Lead starts a term: a new publisher, and every open session is woken so
// it can send the new term's first publication once there is one. Calling
// it again with the same term is a no-op.
func (server *Server) Lead(controlEpoch uint64) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.publisher != nil && server.publisher.epoch == controlEpoch {
		return nil
	}
	publisher, err := NewPublisher(controlEpoch, server.now)
	if err != nil {
		return err
	}
	server.publisher = publisher
	for _, sess := range server.sessions {
		sess.poke()
	}
	return nil
}

// StepDown ends the term: sessions are refused with NOT_LEADER and closed,
// and nothing is published until Lead is called again.
func (server *Server) StepDown() {
	server.mu.Lock()
	server.publisher = nil
	sessions := server.sessions
	server.sessions = map[string]*session{}
	server.mu.Unlock()
	for _, sess := range sessions {
		sess.refuse(RefusalNotLeader)
	}
}

// Close refuses every session with SHUTDOWN; the server accepts no more.
func (server *Server) Close() {
	server.mu.Lock()
	server.closed = true
	server.publisher = nil
	sessions := server.sessions
	server.sessions = map[string]*session{}
	server.mu.Unlock()
	for _, sess := range sessions {
		sess.refuse(RefusalShutdown)
	}
}

// Publish hands the term's publisher a desired set and wakes every session
// when the revision moved. The outcome is reported once per publication
// with the revision, the Workers affected and the ledger of the version it
// pushed out, if any.
func (server *Server) Publish(ctx context.Context, desired Desired) (Published, error) {
	server.mu.Lock()
	publisher := server.publisher
	server.mu.Unlock()
	if publisher == nil {
		return Published{}, errors.New("alarmd viewstream: not leading")
	}
	published, err := publisher.Publish(desired)
	if err != nil {
		return Published{}, err
	}
	server.mu.Lock()
	if published.Changed {
		server.counters.publications++
	} else {
		server.counters.publicationsSkipped++
	}
	sessions := make([]*session, 0, len(server.sessions))
	for _, sess := range server.sessions {
		sessions = append(sessions, sess)
	}
	server.mu.Unlock()
	if !published.Changed {
		return published, nil
	}
	for _, sess := range sessions {
		sess.poke()
	}
	facts := &observability.ViewStreamFacts{Event: "published", ControlEpoch: published.Version.ControlEpoch,
		Revision: published.Version.Revision, Affected: len(published.Affected)}
	for _, closed := range published.Closed {
		facts.Closed = closed.Version.Revision
		facts.Expected, facts.Sent, facts.Acked, facts.Installed, facts.Switched =
			closed.Counts.Expected, closed.Counts.Sent, closed.Counts.Acked, closed.Counts.Installed, closed.Counts.Switched
		if closed.Reason != "" {
			facts.Reason = closed.Reason
		}
	}
	server.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageViewPublished,
		Result: observability.ResultSuccess, ViewStream: facts,
	})
	return published, nil
}

// Stats for the page and the metrics.
func (server *Server) Stats() Stats {
	server.mu.Lock()
	defer server.mu.Unlock()
	stats := Stats{Sessions: len(server.sessions),
		Publications: server.counters.publications, PublicationsSkipped: server.counters.publicationsSkipped,
		SnapshotChunksSent: server.counters.snapshotChunks, DeltasSent: server.counters.deltas,
		EmptyDeltasSent: server.counters.emptyDeltas, Refusals: server.counters.refusals}
	if server.publisher == nil {
		return stats
	}
	stats.Leading, stats.ControlEpoch, stats.Revision = true, server.publisher.epoch, server.publisher.Revision()
	if key, counts, ok := server.publisher.ledger.Current(); ok {
		stats.Current, stats.Counts = key, counts
		stats.Objects, _ = server.publisher.ledger.Objects(key)
		stats.Lagging = server.publisher.ledger.Lagging("installed")
		for index := range stats.Lagging {
			_, connected := server.sessions[stats.Lagging[index].WorkerID]
			stats.Lagging[index].Connected = connected
		}
	}
	stats.Ignored = server.publisher.ledger.Ignored()
	return stats
}

func (server *Server) currentPublisher() *Publisher {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.publisher
}

// Connect serves one Worker's stream: Hello first, admission, then the
// send loop for as long as the stream, the term and the Worker's heartbeats
// last.
func (server *Server) Connect(stream pb.ControlService_ConnectServer) error {
	ctx := stream.Context()
	hello, err := awaitHello(ctx, stream)
	if err != nil {
		server.count(func(c *serverCounters) { c.refusals++ })
		return server.refuseStream(ctx, stream, Receiver{}, RefusalHelloExpected)
	}
	receiver := Receiver{WorkerID: hello.WorkerId, Incarnation: hello.Incarnation}
	if hello.ProtocolVersion != ProtocolVersion {
		server.count(func(c *serverCounters) { c.refusals++ })
		return server.refuseStream(ctx, stream, receiver, RefusalProtocolVersion)
	}
	if reason := server.admit(ctx, hello); reason != "" {
		server.count(func(c *serverCounters) { c.refusals++ })
		return server.refuseStream(ctx, stream, receiver, reason)
	}
	// What the Hello says is installed counts as sent and installed for
	// this session -- a Worker that reconnects one revision behind gets the
	// one-step delta, not the whole snapshot again -- and for the ledger:
	// the receipt that said so may have been lost with the stream, and a
	// version the ledger still follows must not stay short one receiver
	// for a Worker that holds it. The claim goes through Record like a
	// receipt, so a digest that is not this Worker's, or a version no
	// longer followed, is refused the same way; what the Hello cannot
	// say -- the objects missing -- is left unprobed, not 0.
	installed := versionFromWire(hello.Installed)
	if !server.recordClaimedInstall(ctx, receiver, installed) {
		// A claim the publisher cannot vouch for: the session starts from
		// nothing and the Worker gets a snapshot.
		installed = Version{}
	}
	sess := &session{server: server, stream: stream, receiver: receiver, wake: make(chan struct{}, 1),
		outbound: make(chan *pb.LeaderMessage, 16), done: make(chan struct{}),
		installed: installed, sent: installed, lastHeard: server.now()}
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return server.refuseStream(ctx, stream, receiver, RefusalShutdown)
	}
	previous := server.sessions[receiver.WorkerID]
	server.sessions[receiver.WorkerID] = sess
	server.mu.Unlock()
	if previous != nil {
		previous.refuse(RefusalReplaced)
	}
	server.observeSession(ctx, "opened", receiver, "")
	// The first turn of the send loop is now, not at the next publication
	// or idle tick: a Worker that connects between publications gets its
	// snapshot at once.
	sess.poke()
	go sess.receive()
	reason := sess.send()
	server.mu.Lock()
	if server.sessions[receiver.WorkerID] == sess {
		delete(server.sessions, receiver.WorkerID)
	}
	server.mu.Unlock()
	server.observeSession(ctx, "closed", receiver, reason)
	return nil
}

// recordClaimedInstall credits a Worker's Hello with the version it says
// it holds, when that is a view this publisher gave it: sent and
// installed, objects unprobed. False when the claim is not one the
// publisher can vouch for -- another term, a digest that is not this
// Worker's, a revision no longer kept -- and the caller starts the session
// from nothing. The ledger's own refusals still apply on the way.
func (server *Server) recordClaimedInstall(ctx context.Context, receiver Receiver, installed Version) bool {
	if installed.Revision == 0 {
		return false
	}
	publisher := server.currentPublisher()
	if publisher == nil || installed.ControlEpoch != publisher.epoch {
		return false
	}
	if !publisher.Holds(receiver.WorkerID, installed) {
		publisher.ledger.countDigestMismatch()
		return false
	}
	if !publisher.ledger.MarkSent(installed.Key(), receiver) {
		return false
	}
	recorded := publisher.ledger.Record(Receipt{Receiver: receiver, Version: installed, Acked: true, Installed: true})
	if recorded.InstalledByAll {
		server.observeInstalledByAll(ctx, recorded)
	}
	return recorded.Attributed
}

func (server *Server) admit(ctx context.Context, hello *pb.Hello) string {
	if hello.WorkerId == "" || hello.Incarnation == "" {
		return RefusalUnknownWorker
	}
	if server.currentPublisher() == nil {
		return RefusalNotLeader
	}
	reason, err := server.admission.Admit(ctx, hello.WorkerId, hello.StreamToken)
	if err != nil {
		return RefusalRegistryUnavailable
	}
	return reason
}

func (server *Server) refuseStream(ctx context.Context, stream pb.ControlService_ConnectServer, receiver Receiver, reason string) error {
	var epoch uint64
	if publisher := server.currentPublisher(); publisher != nil {
		epoch = publisher.epoch
	}
	_ = stream.Send(&pb.LeaderMessage{Body: &pb.LeaderMessage_Refusal{Refusal: &pb.Refusal{Reason: reason, ControlEpoch: epoch}}})
	server.observeSession(ctx, "refused", receiver, reason)
	return nil
}

func (server *Server) observeSession(ctx context.Context, event string, receiver Receiver, reason string) {
	var epoch uint64
	if publisher := server.currentPublisher(); publisher != nil {
		epoch = publisher.epoch
	}
	result := observability.Result(observability.ResultSuccess)
	if event == "refused" {
		result = observability.ResultDegraded
	}
	server.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageViewSession, Result: result,
		ReasonCode: observability.ViewStreamReasonCode(reason),
		ViewStream: &observability.ViewStreamFacts{Event: event, WorkerID: receiver.WorkerID, Incarnation: receiver.Incarnation,
			ControlEpoch: epoch, Reason: reason},
	})
}

// observeInstalledByAll reports the moment a version is installed by every
// receiver it expected, with how long that took from its publication: the
// fleet's time to hold one publication, per publication, on the line.
func (server *Server) observeInstalledByAll(ctx context.Context, recorded Recorded) {
	server.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageViewPublished,
		Result: observability.ResultSuccess, Duration: recorded.Elapsed,
		ViewStream: &observability.ViewStreamFacts{Event: "installed_by_all", ControlEpoch: recorded.Version.ControlEpoch,
			Revision: recorded.Version.Revision, Expected: recorded.Expected, Installed: recorded.Expected},
	})
}

func (server *Server) count(update func(*serverCounters)) {
	server.mu.Lock()
	update(&server.counters)
	server.mu.Unlock()
}

// awaitHello reads the first message, which must be a Hello, within
// HelloTimeout.
func awaitHello(ctx context.Context, stream pb.ControlService_ConnectServer) (*pb.Hello, error) {
	type received struct {
		message *pb.WorkerMessage
		err     error
	}
	first := make(chan received, 1)
	go func() {
		message, err := stream.Recv()
		first <- received{message: message, err: err}
	}()
	timer := time.NewTimer(HelloTimeout)
	defer timer.Stop()
	select {
	case got := <-first:
		if got.err != nil {
			return nil, got.err
		}
		hello := got.message.GetHello()
		if hello == nil {
			return nil, errors.New("alarmd viewstream: first message is not a Hello")
		}
		return hello, nil
	case <-timer.C:
		return nil, errors.New("alarmd viewstream: no Hello within the timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// session is one Worker's stream on the Leader.
type session struct {
	server   *Server
	stream   pb.ControlService_ConnectServer
	receiver Receiver
	wake     chan struct{}
	outbound chan *pb.LeaderMessage
	done     chan struct{}
	closeOne sync.Once

	mu           sync.Mutex
	installed    Version
	sent         Version
	wantSnapshot bool
	lastHeard    time.Time
	refusal      string
}

func (sess *session) poke() {
	select {
	case sess.wake <- struct{}{}:
	default:
	}
}

// refuse ends the session with a reason; the send loop delivers the
// Refusal and returns.
func (sess *session) refuse(reason string) {
	sess.mu.Lock()
	if sess.refusal == "" {
		sess.refusal = reason
	}
	sess.mu.Unlock()
	sess.poke()
}

func (sess *session) finish() {
	sess.closeOne.Do(func() { close(sess.done) })
}

// receive reads the Worker's messages until the stream ends.
func (sess *session) receive() {
	defer sess.finish()
	for {
		message, err := sess.stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && sess.stream.Context().Err() == nil {
				sess.refuse("RECV_FAILED")
			}
			return
		}
		sess.mu.Lock()
		sess.lastHeard = sess.server.now()
		sess.mu.Unlock()
		switch body := message.Body.(type) {
		case *pb.WorkerMessage_Receipt:
			receipt, err := ReceiptFromWire(sess.receiver.WorkerID, body.Receipt)
			if err != nil {
				continue
			}
			if publisher := sess.server.currentPublisher(); publisher != nil {
				if recorded := publisher.ledger.Record(receipt); recorded.InstalledByAll {
					sess.server.observeInstalledByAll(sess.stream.Context(), recorded)
				}
			}
			if receipt.Installed {
				sess.mu.Lock()
				sess.installed = receipt.Version
				sess.mu.Unlock()
				sess.poke()
			}
		case *pb.WorkerMessage_SnapshotRequest:
			sess.mu.Lock()
			sess.wantSnapshot = true
			if body.SnapshotRequest.Installed != nil {
				sess.installed = versionFromWire(body.SnapshotRequest.Installed)
			}
			sess.mu.Unlock()
			sess.poke()
		case *pb.WorkerMessage_Heartbeat:
			sess.enqueue(&pb.LeaderMessage{Body: &pb.LeaderMessage_Heartbeat{Heartbeat: &pb.Heartbeat{
				SentAtMs: sess.server.now().UnixMilli(), Installed: versionToWire(sess.currentSent())}}})
		case *pb.WorkerMessage_ObjectRequest:
			sess.enqueue(&pb.LeaderMessage{Body: &pb.LeaderMessage_ObjectResult{ObjectResult: &pb.ObjectResult{
				Kind: body.ObjectRequest.Kind, Digest: body.ObjectRequest.Digest, Unavailable: ObjectUnavailableUnsupported}}})
		case *pb.WorkerMessage_Hello:
			// A second Hello on an open stream is a protocol error.
			sess.refuse(RefusalHelloExpected)
			return
		}
	}
}

func (sess *session) enqueue(message *pb.LeaderMessage) {
	select {
	case sess.outbound <- message:
	case <-sess.done:
	case <-sess.stream.Context().Done():
	}
}

func (sess *session) currentSent() Version {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.sent
}

// send is the session's one writer. It returns the reason the session
// ended.
func (sess *session) send() string {
	defer sess.finish()
	ticker := time.NewTicker(sess.server.tick)
	defer ticker.Stop()
	ctx := sess.stream.Context()
	for {
		select {
		case <-ctx.Done():
			return "STREAM_CLOSED"
		case <-sess.done:
			return sess.refusalOr("STREAM_CLOSED")
		case message := <-sess.outbound:
			if err := sess.stream.Send(message); err != nil {
				return "SEND_FAILED"
			}
			continue
		case <-ticker.C:
			sess.mu.Lock()
			idle := sess.server.now().Sub(sess.lastHeard) > IdleTimeout
			sess.mu.Unlock()
			if idle {
				sess.refuse(RefusalIdle)
			}
		case <-sess.wake:
		}
		if reason := sess.refusalOr(""); reason != "" {
			_ = sess.stream.Send(&pb.LeaderMessage{Body: &pb.LeaderMessage_Refusal{Refusal: &pb.Refusal{Reason: reason}}})
			return reason
		}
		publisher := sess.server.currentPublisher()
		if publisher == nil {
			_ = sess.stream.Send(&pb.LeaderMessage{Body: &pb.LeaderMessage_Refusal{Refusal: &pb.Refusal{Reason: RefusalNotLeader}}})
			return RefusalNotLeader
		}
		if err := sess.publish(publisher); err != nil {
			return "SEND_FAILED"
		}
	}
}

func (sess *session) refusalOr(fallback string) string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.refusal != "" {
		return sess.refusal
	}
	return fallback
}

// publish brings the Worker to the publisher's current revision if it is
// not there: one delta when the Worker installed exactly the previous
// revision and nothing else is pending, the snapshot otherwise.
func (sess *session) publish(publisher *Publisher) error {
	revision := publisher.Revision()
	if revision == 0 {
		return nil
	}
	sess.mu.Lock()
	installed, sent, wantSnapshot := sess.installed, sess.sent, sess.wantSnapshot
	sess.mu.Unlock()
	if sent.Revision == revision && sent.ControlEpoch == publisher.epoch && !wantSnapshot {
		return nil
	}
	if !wantSnapshot && installed.ControlEpoch == publisher.epoch && installed == sent {
		if delta, ok := publisher.Step(sess.receiver.WorkerID, installed); ok {
			if err := sess.stream.Send(&pb.LeaderMessage{Body: &pb.LeaderMessage_Delta{Delta: DeltaToWire(delta)}}); err != nil {
				return err
			}
			publisher.ledger.MarkSent(delta.Target.Key(), sess.receiver)
			sess.server.count(func(c *serverCounters) {
				if delta.Empty() {
					c.emptyDeltas++
				} else {
					c.deltas++
				}
			})
			sess.mu.Lock()
			sess.sent = delta.Target
			sess.mu.Unlock()
			return nil
		}
	}
	snapshot, ok := publisher.Snapshot(sess.receiver.WorkerID)
	if !ok {
		return nil
	}
	chunks := SnapshotChunks(snapshot, 0)
	for _, chunk := range chunks {
		if err := sess.stream.Send(&pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}}); err != nil {
			return err
		}
	}
	publisher.ledger.MarkSent(snapshot.Version.Key(), sess.receiver)
	sess.server.count(func(c *serverCounters) { c.snapshotChunks += uint64(len(chunks)) })
	sess.mu.Lock()
	sess.sent, sess.wantSnapshot = snapshot.Version, false
	sess.mu.Unlock()
	return nil
}
