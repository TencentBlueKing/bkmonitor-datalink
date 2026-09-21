// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// The Worker's side of the stream, in the shadow step of decision-016
// section 7.1: it finds the Leader, connects, says Hello, installs what it
// is sent -- whole snapshots and one-step deltas, each verified against its
// digest before anything is installed -- and reports back per version.
// Nothing executes off the installed view. A Leader that cannot be found,
// refuses the stream, or sends something that does not verify costs this
// Worker a log line and a counter and nothing else.

// Reconnect pacing: exponential from ReconnectMin to ReconnectMax with full
// jitter, so sixty-four Workers cut off by one Leader restart do not come
// back as one wave every doubling.
const (
	ReconnectMin = time.Second
	ReconnectMax = 30 * time.Second
)

// DisconnectLeaderSilent is why a Worker cut a stream on which the Leader
// answered nothing -- no heartbeat reply, no publication -- for
// IdleTimeout: the mirror of the Leader's IDLE rule. Without it a Leader
// that stopped answering would hold the Worker's only stream until TCP
// gave up.
const DisconnectLeaderSilent = "LEADER_SILENT"

// Install failure words, the bounded vocabulary of Receipt.failure and
// SnapshotRequest.reason.
const (
	FailureDeltaBaseMismatch  = "DELTA_BASE_MISMATCH"
	FailureDeltaDigest        = "DELTA_DIGEST_MISMATCH"
	FailureSnapshotInvalid    = "SNAPSHOT_INVALID"
	FailureSnapshotIncomplete = "SNAPSHOT_INCOMPLETE"
	FailureWrongWorker        = "VIEW_FOR_ANOTHER_WORKER"
)

// LeaderEndpoint is where the Leader's stream is served, as discovery
// found it: the Leader's worker id, its term, and the endpoint its
// registration advertises.
type LeaderEndpoint struct {
	WorkerID     string
	ControlEpoch uint64
	Endpoint     string
}

// Discovery answers who leads and where. False with no error is "no
// Leader, or a Leader that advertises no endpoint" -- a binary from before
// the stream -- which the client waits out.
type Discovery interface {
	// Leader returns the Leader to connect to, or the reason there is none
	// (one of the Miss words) and no error, or an error when the lookup
	// itself failed.
	Leader(context.Context) (LeaderEndpoint, string, error)
}

// The ways a discovery finds no Leader, each a cell of its own on the miss
// counter: they are read by different people and mended in different
// places. DISCOVERY_FAILED is the lookup erroring.
const (
	MissNoLeader           = "NO_LEADER"
	MissLeaderUnregistered = "LEADER_UNREGISTERED"
	MissLeaderNoEndpoint   = "LEADER_NO_ENDPOINT"
	MissDiscoveryFailed    = "DISCOVERY_FAILED"
)

// DiscoveryMissReasons is the closed set, for the metric.
var DiscoveryMissReasons = []string{MissNoLeader, MissLeaderUnregistered, MissLeaderNoEndpoint, MissDiscoveryFailed}

// ObjectProbe says how many of a view's objects the Worker cannot read
// from its catalog. Asked about the whole installed view at every install
// and again on every heartbeat: an object missing since the snapshot stays
// missing through deltas that touch other Query Groups, and an object that
// expired or was evicted between publications goes missing with no install
// at all -- only a periodic look finds it. The probe serves from the cache
// first, so a whole view of held objects costs no round trip, and a
// heartbeat's re-probe of a healthy Worker touches Redis not at all.
type ObjectProbe interface {
	MissingObjects(ctx context.Context, objects []execution.ObjectDigest, contexts []execution.OutputContextDigest) (int, error)
}

// ClientIdentity is what the Worker presents: its id, the incarnation of
// this process, and the token it wrote into its registration.
type ClientIdentity struct {
	WorkerID    string
	Incarnation string
	StreamToken string
}

// ClientStats is the Worker's account of the stream for its metrics.
type ClientStats struct {
	Connected   bool
	Leader      LeaderEndpoint
	Installed   Version
	InstalledAt time.Time
	// ObjectsMissing is of the whole installed view, meaningful only when
	// ObjectsProbed: a probe that failed leaves the count unknown, not 0.
	ObjectsMissing int
	ObjectsProbed  bool
	// Counters since the process started. Installs is by kind: snapshot,
	// delta, empty_delta.
	Installs           map[string]uint64
	InstallFailures    map[string]uint64
	SnapshotsRequested uint64
	Refusals           map[string]uint64
	Connections        uint64
	// DiscoveryMisses is by reason, one of DiscoveryMissReasons.
	DiscoveryMisses map[string]uint64
}

// ClientOptions are the client's seams: the dialer a test replaces to reach
// an in-memory Leader, a clock, and how often the heartbeat and the
// Leader-silence check run against it.
type ClientOptions struct {
	Dial func(ctx context.Context, endpoint string) (pb.ControlServiceClient, func() error, error)
	Now  func() time.Time
	// Sleep replaces the reconnect wait; a test drives it.
	Sleep func(ctx context.Context, wait time.Duration) error
	Tick  time.Duration
}

// Client keeps one stream to the Leader and the view it installed.
type Client struct {
	identity  ClientIdentity
	discovery Discovery
	probe     ObjectProbe
	observer  observability.Observer
	dial      func(ctx context.Context, endpoint string) (pb.ControlServiceClient, func() error, error)
	now       func() time.Time
	sleep     func(ctx context.Context, wait time.Duration) error
	tick      time.Duration
	random    *rand.Rand

	installed atomic.Pointer[View]
	mu        sync.Mutex
	stats     ClientStats
}

func NewClient(identity ClientIdentity, discovery Discovery, probe ObjectProbe, observer observability.Observer, options ClientOptions) (*Client, error) {
	if identity.WorkerID == "" || identity.Incarnation == "" {
		return nil, errors.New("alarmd viewstream: client needs a worker id and an incarnation")
	}
	if discovery == nil {
		return nil, errors.New("alarmd viewstream: client needs a discovery")
	}
	if observer == nil {
		observer = observability.ObserverFunc(func(context.Context, observability.Observation) {})
	}
	client := &Client{identity: identity, discovery: discovery, probe: probe, observer: observer,
		dial: options.Dial, now: options.Now, sleep: options.Sleep, tick: options.Tick,
		random: rand.New(rand.NewSource(time.Now().UnixNano())),
		stats:  ClientStats{Installs: map[string]uint64{}, InstallFailures: map[string]uint64{}, Refusals: map[string]uint64{}}}
	if client.dial == nil {
		client.dial = dialLeader
	}
	if client.now == nil {
		client.now = time.Now
	}
	if client.tick <= 0 {
		client.tick = HeartbeatInterval
	}
	if client.sleep == nil {
		client.sleep = func(ctx context.Context, wait time.Duration) error {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return client, nil
}

// dialLeader opens a plaintext HTTP/2 connection to the endpoint: the
// stream shares the Leader's HTTP listener, which speaks h2c. Keepalive is
// the client's to run -- the Leader serves through http.Server and cannot
// set it -- and it is the transport-level backstop under the protocol's
// own heartbeat: a peer that vanished without a FIN is found by the ping
// within Time + Timeout, and the stream ends instead of waiting out TCP.
func dialLeader(_ context.Context, endpoint string) (pb.ControlServiceClient, func() error, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 3 * HeartbeatInterval, Timeout: HeartbeatInterval}))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewControlServiceClient(conn), conn.Close, nil
}

// Installed is the view this Worker holds, if any. Read by the metrics
// and, in the shadow step, by nothing else.
func (client *Client) Installed() (View, bool) {
	view := client.installed.Load()
	if view == nil {
		return View{}, false
	}
	return *view, true
}

// Stats for the metrics.
func (client *Client) Stats() ClientStats {
	client.mu.Lock()
	defer client.mu.Unlock()
	stats := client.stats
	stats.Installs = copyCounts(client.stats.Installs)
	stats.InstallFailures = copyCounts(client.stats.InstallFailures)
	stats.Refusals = copyCounts(client.stats.Refusals)
	stats.DiscoveryMisses = copyCounts(client.stats.DiscoveryMisses)
	return stats
}

func copyCounts(counts map[string]uint64) map[string]uint64 {
	copied := make(map[string]uint64, len(counts))
	for key, value := range counts {
		copied[key] = value
	}
	return copied
}

// Run keeps the stream up until ctx ends: discover, connect, serve the
// stream until it ends, wait with jitter, again. It returns ctx's error and
// nothing else; every failure on the way is reported and retried.
func (client *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connected, err := client.serveOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			attempt = 0
		}
		if err != nil || !connected {
			attempt++
		}
		if err := client.sleep(ctx, client.backoff(attempt)); err != nil {
			return err
		}
	}
}

// backoff is full jitter over an exponential ceiling: uniform in
// [0, min(ReconnectMax, ReconnectMin * 2^(attempt-1))].
func (client *Client) backoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	ceiling := ReconnectMin
	for i := 1; i < attempt && ceiling < ReconnectMax; i++ {
		ceiling *= 2
	}
	if ceiling > ReconnectMax {
		ceiling = ReconnectMax
	}
	client.mu.Lock()
	wait := time.Duration(client.random.Int63n(int64(ceiling)))
	client.mu.Unlock()
	return wait
}

// serveOnce runs one connection to its end. connected says whether a
// stream was opened and admitted; err why it ended, nil for a clean end.
func (client *Client) serveOnce(ctx context.Context) (connected bool, err error) {
	leader, miss, err := client.discovery.Leader(ctx)
	if err != nil {
		miss = MissDiscoveryFailed
	} else if miss == "" && leader.Endpoint == "" {
		miss = MissLeaderNoEndpoint
	}
	if miss != "" {
		client.mu.Lock()
		if client.stats.DiscoveryMisses == nil {
			client.stats.DiscoveryMisses = make(map[string]uint64, len(DiscoveryMissReasons))
		}
		client.stats.DiscoveryMisses[miss]++
		client.mu.Unlock()
		client.observe(ctx, "discovery_missed", miss, err)
		return false, err
	}
	service, closeConn, err := client.dial(ctx, leader.Endpoint)
	if err != nil {
		client.observe(ctx, "dial_failed", leader.Endpoint, err)
		return false, err
	}
	defer func() { _ = closeConn() }()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := service.Connect(streamCtx)
	if err != nil {
		client.observe(ctx, "connect_failed", leader.Endpoint, err)
		return false, err
	}
	installed, _ := client.Installed()
	hello := &pb.Hello{WorkerId: client.identity.WorkerID, Incarnation: client.identity.Incarnation,
		ProtocolVersion: ProtocolVersion, StreamToken: client.identity.StreamToken}
	if installed.Version.Revision > 0 {
		hello.Installed = versionToWire(installed.Version)
	}
	if err := stream.Send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: hello}}); err != nil {
		client.observe(ctx, "hello_failed", leader.Endpoint, err)
		return false, err
	}
	client.mu.Lock()
	client.stats.Connected, client.stats.Leader, client.stats.Connections = true, leader, client.stats.Connections+1
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		client.stats.Connected = false
		client.mu.Unlock()
	}()
	client.observe(ctx, "connected", leader.Endpoint, nil)
	reason, err := client.session(streamCtx, stream, cancel)
	client.observe(ctx, "disconnected", reason, err)
	return true, err
}

// session reads the stream until it ends and installs what arrives. The
// heartbeat runs beside it. Returns why the stream ended.
func (client *Client) session(ctx context.Context, stream pb.ControlService_ConnectClient, cancel context.CancelFunc) (string, error) {
	var sendMu sync.Mutex
	send := func(message *pb.WorkerMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(message)
	}
	var lastHeard atomic.Int64
	lastHeard.Store(client.now().UnixNano())
	var leaderSilent atomic.Bool
	heartbeats := make(chan struct{})
	go func() {
		defer close(heartbeats)
		ticker := time.NewTicker(client.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if client.now().Sub(time.Unix(0, lastHeard.Load())) > IdleTimeout {
					leaderSilent.Store(true)
					cancel()
					return
				}
				installed, has := client.Installed()
				if err := send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{
					SentAtMs: client.now().UnixMilli(), Installed: versionToWire(installed.Version)}}}); err != nil {
					cancel()
					return
				}
				if has {
					client.reprobe(ctx, installed, send)
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeats }()
	var pending []*pb.Snapshot
	for {
		message, err := stream.Recv()
		if err != nil {
			if leaderSilent.Load() {
				return DisconnectLeaderSilent, errors.New("alarmd viewstream: the Leader answered nothing for " + IdleTimeout.String())
			}
			if ctx.Err() != nil {
				return "STREAM_CLOSED", nil
			}
			return "RECV_FAILED", err
		}
		lastHeard.Store(client.now().UnixNano())
		switch body := message.Body.(type) {
		case *pb.LeaderMessage_Refusal:
			reason := body.Refusal.Reason
			client.mu.Lock()
			client.stats.Refusals[reason]++
			client.mu.Unlock()
			return reason, fmt.Errorf("alarmd viewstream: refused: %s", reason)
		case *pb.LeaderMessage_Snapshot:
			chunk := body.Snapshot
			if len(pending) > 0 && (pending[0].Version == nil || chunk.Version == nil || versionFromWire(pending[0].Version) != versionFromWire(chunk.Version)) {
				// A newer snapshot started before the last was complete: the
				// old chunks are dropped, the new one is what the Leader means.
				client.countInstallFailure(FailureSnapshotIncomplete)
				pending = nil
			}
			pending = append(pending, chunk)
			if chunk.Chunks == 0 || len(pending) < int(chunk.Chunks) {
				continue
			}
			chunks := pending
			pending = nil
			client.installSnapshot(ctx, chunks, send)
		case *pb.LeaderMessage_Delta:
			client.installDelta(ctx, body.Delta, send)
		case *pb.LeaderMessage_Heartbeat, *pb.LeaderMessage_ObjectResult:
			// Nothing to do with either in the shadow step.
		}
	}
}

func (client *Client) installSnapshot(ctx context.Context, chunks []*pb.Snapshot, send func(*pb.WorkerMessage) error) {
	view, err := AssembleSnapshot(client.identity.WorkerID, chunks)
	if err != nil {
		client.countInstallFailure(FailureSnapshotInvalid)
		client.observe(ctx, "install_refused", FailureSnapshotInvalid, err)
		version := versionFromWire(chunks[0].Version)
		_ = send(receiptMessage(client.identity.Incarnation, version, true, false, FailureSnapshotInvalid, 0, false))
		client.requestSnapshot(send, FailureSnapshotInvalid)
		return
	}
	missing, probed := client.probeMissing(ctx, view.Entries)
	client.install(ctx, view, missing, probed, "snapshot")
	_ = send(receiptMessage(client.identity.Incarnation, view.Version, true, true, "", missing, probed))
}

func (client *Client) installDelta(ctx context.Context, wire *pb.Delta, send func(*pb.WorkerMessage) error) {
	delta, err := DeltaFromWire(client.identity.WorkerID, wire)
	if err != nil {
		client.countInstallFailure(FailureDeltaDigest)
		client.observe(ctx, "install_refused", FailureDeltaDigest, err)
		client.requestSnapshot(send, FailureDeltaDigest)
		return
	}
	installed, has := client.Installed()
	if !has {
		installed = View{WorkerID: client.identity.WorkerID}
	}
	next, err := Apply(installed, delta)
	if err != nil {
		reason := FailureDeltaDigest
		if errors.Is(err, ErrDeltaBaseMismatch) {
			reason = FailureDeltaBaseMismatch
		}
		client.countInstallFailure(reason)
		client.observe(ctx, "install_refused", reason, err)
		_ = send(receiptMessage(client.identity.Incarnation, delta.Target, true, false, reason, 0, false))
		client.requestSnapshot(send, reason)
		return
	}
	// The whole view is probed, not the upserts: what went missing since
	// the last install -- an object evicted or expired under a Query Group
	// this delta did not touch -- is found here or nowhere.
	missing, probed := client.probeMissing(ctx, next.Entries)
	kind := "delta"
	if delta.Empty() {
		kind = "empty_delta"
	}
	client.install(ctx, next, missing, probed, kind)
	_ = send(receiptMessage(client.identity.Incarnation, next.Version, true, true, "", missing, probed))
}

func (client *Client) requestSnapshot(send func(*pb.WorkerMessage) error, reason string) {
	client.mu.Lock()
	client.stats.SnapshotsRequested++
	client.mu.Unlock()
	installed, _ := client.Installed()
	request := &pb.SnapshotRequest{Reason: reason}
	if installed.Version.Revision > 0 {
		request.Installed = versionToWire(installed.Version)
	}
	_ = send(&pb.WorkerMessage{Body: &pb.WorkerMessage_SnapshotRequest{SnapshotRequest: request}})
}

// probeMissing asks the catalog about the objects entries name. Without a
// probe, or when the probe fails, the count is unknown: false, with the
// failure observed, and never 0 in its place. A view naming no objects is
// probed trivially, 0 and true.
func (client *Client) probeMissing(ctx context.Context, entries []Entry) (int, bool) {
	if client.probe == nil {
		return 0, false
	}
	var objects []execution.ObjectDigest
	var contexts []execution.OutputContextDigest
	for _, entry := range entries {
		if entry.Content == nil {
			continue
		}
		objects = append(objects, entry.Content.ObjectDigest)
		for _, ref := range entry.Content.OutputContexts {
			contexts = append(contexts, ref.Digest)
		}
	}
	if len(objects) == 0 && len(contexts) == 0 {
		return 0, true
	}
	missing, err := client.probe.MissingObjects(ctx, objects, contexts)
	if err != nil {
		client.observe(ctx, "object_probe_failed", "", err)
		return 0, false
	}
	return missing, true
}

// reprobe asks the catalog about the installed view again, on the
// heartbeat, and tells the Leader when the answer changed: a receipt for
// the same version with the new count, which the ledger takes as the
// current objects-missing of an installed receiver and nothing more.
func (client *Client) reprobe(ctx context.Context, installed View, send func(*pb.WorkerMessage) error) {
	if client.probe == nil {
		return
	}
	missing, probed := client.probeMissing(ctx, installed.Entries)
	client.mu.Lock()
	current := client.stats.Installed == installed.Version
	same := client.stats.ObjectsMissing == missing && client.stats.ObjectsProbed == probed
	if current {
		client.stats.ObjectsMissing, client.stats.ObjectsProbed = missing, probed
	}
	client.mu.Unlock()
	// A newer version was installed while the probe ran: its own install
	// probed it, and a receipt for the version just left would only land in
	// the Leader's unknown_version count and read like a fault.
	if !current || same {
		return
	}
	client.observe(ctx, "objects_reprobed", fmt.Sprintf("missing=%d probed=%t", missing, probed), nil)
	_ = send(receiptMessage(client.identity.Incarnation, installed.Version, true, true, "", missing, probed))
}

// install swaps the view in atomically and records the install.
func (client *Client) install(ctx context.Context, view View, missing int, probed bool, kind string) {
	copied := view
	client.installed.Store(&copied)
	client.mu.Lock()
	client.stats.Installed, client.stats.InstalledAt = view.Version, client.now()
	client.stats.ObjectsMissing, client.stats.ObjectsProbed = missing, probed
	client.stats.Installs[kind]++
	client.mu.Unlock()
	client.observeInstall(ctx, view, missing, probed)
}

func (client *Client) countInstallFailure(reason string) {
	client.mu.Lock()
	client.stats.InstallFailures[reason]++
	client.mu.Unlock()
}

func receiptMessage(incarnation string, version Version, acked, installed bool, failure string, missing int, probed bool) *pb.WorkerMessage {
	return &pb.WorkerMessage{Body: &pb.WorkerMessage_Receipt{Receipt: ReceiptToWire(Receipt{
		Receiver: Receiver{Incarnation: incarnation}, Version: version, Acked: acked, Installed: installed,
		Failure: failure, ObjectsMissing: missing, ObjectsProbed: probed,
	})}}
}

func (client *Client) observe(ctx context.Context, event, reason string, err error) {
	result := observability.Result(observability.ResultSuccess)
	if err != nil || event == "discovery_missed" || event == "install_refused" {
		result = observability.ResultDegraded
	}
	installed, _ := client.Installed()
	// The reason is the line's reason_code when it is one of the stream's
	// words; an endpoint or a detail stays in the facts.
	client.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageViewSession, Result: result, Err: err,
		ReasonCode: observability.ViewStreamReasonCode(reason),
		ViewStream: &observability.ViewStreamFacts{Event: event, WorkerID: client.identity.WorkerID, Incarnation: client.identity.Incarnation,
			ControlEpoch: installed.Version.ControlEpoch, Revision: installed.Version.Revision, Reason: reason},
	})
}

func (client *Client) observeInstall(ctx context.Context, view View, missing int, probed bool) {
	reason := ""
	if !probed {
		reason = "OBJECTS_NOT_PROBED"
	}
	client.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageViewInstalled, Result: observability.ResultSuccess,
		ViewStream: &observability.ViewStreamFacts{Event: "installed", WorkerID: client.identity.WorkerID, Incarnation: client.identity.Incarnation,
			ControlEpoch: view.Version.ControlEpoch, Revision: view.Version.Revision, Affected: len(view.Entries), ObjectsMissing: missing, Reason: reason},
	})
}
