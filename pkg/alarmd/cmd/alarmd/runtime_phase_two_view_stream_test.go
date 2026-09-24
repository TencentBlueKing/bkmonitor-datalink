// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// The production Leader publishes the round's desired set over the view
// stream and serves it to a Worker that presents the token the Worker's
// own registration carries: the fixture's single replica registers with an
// endpoint and a token, leads, publishes revision 1 with itself as the one
// expected receiver, answers a Hello with a snapshot naming both of its
// Query Groups under the activation it executes, counts the receipt, and
// refuses a Hello with another token. Nothing here reads the view for
// execution: the fixture's Slot completed before any of this.
func TestTheProductionLeaderServesItsDesiredSetOverTheViewStream(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	bundle := fixture.bundle
	if bundle.dependencies.ControlStream == nil || bundle.dependencies.ViewStreamStats == nil {
		t.Fatal("the production bundle has no control stream")
	}
	identity := bundle.dependencies.StreamIdentity
	if identity.Token == "" || identity.Endpoint == "" || !strings.HasSuffix(identity.Endpoint, ":"+strings.TrimPrefix(fixture.cfg.HTTP.Listen[strings.LastIndex(fixture.cfg.HTTP.Listen, ":"):], ":")) {
		t.Fatalf("stream identity = %+v, want a token and an endpoint on the HTTP listener's port %s", identity, fixture.cfg.HTTP.Listen)
	}
	// The registration in Redis carries both, and the token is what the
	// Leader checks a Hello against.
	store, err := ownership.NewRedisStoreWithClient(fixture.redisClient, productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	registration, found, err := store.ReadWorker(ctx, fixture.cfg.PhaseTwo.Worker.ID)
	if err != nil || !found || registration.Endpoint != identity.Endpoint || registration.StreamToken != identity.Token {
		t.Fatalf("registration = %+v found=%t err=%v, want endpoint %s and the process token", registration, found, err, identity.Endpoint)
	}
	leader, found, err := store.ReadControlLeader(ctx)
	if err != nil || !found || leader.OwnerID != fixture.cfg.PhaseTwo.Worker.ID || leader.OwnerEpoch == 0 {
		t.Fatalf("control leader = %+v found=%t err=%v, want this replica with a term", leader, found, err)
	}

	// The fixture serves the control stream once it runs a Slot, so this
	// replica's own Worker may already hold a session; the manual stream
	// below replaces it under the same Worker id.
	stats := bundle.dependencies.ViewStreamStats()
	if !stats.Leading || stats.ControlEpoch != leader.OwnerEpoch || stats.Revision == 0 || stats.Counts.Expected != 1 || stats.Sessions > 1 {
		t.Fatalf("stream stats after the first round = %+v, want leading in term %d with a revision and one expected receiver", stats, leader.OwnerEpoch)
	}

	// The stream served the way the listener serves it: over HTTP/2 in the
	// clear, on a handler beside the mux.
	listener := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor == 2 && strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
			bundle.dependencies.ControlStream.ServeHTTP(response, request)
			return
		}
		http.NotFound(response, request)
	}), &http2.Server{}))
	defer listener.Close()
	conn, err := grpc.NewClient(strings.TrimPrefix(listener.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewControlServiceClient(conn)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// The wrong token is refused before anything is served.
	wrong, err := client.Connect(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	hello := func(token string) *pb.WorkerMessage {
		return &pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{
			WorkerId: fixture.cfg.PhaseTwo.Worker.ID, Incarnation: "test-incarnation", ProtocolVersion: viewstream.ProtocolVersion, StreamToken: token,
		}}}
	}
	if err := wrong.Send(hello("not-the-token")); err != nil {
		t.Fatal(err)
	}
	if reply, err := wrong.Recv(); err != nil || reply.GetRefusal() == nil || reply.GetRefusal().Reason != viewstream.RefusalBadToken {
		t.Fatalf("wrong token: reply=%+v err=%v, want BAD_TOKEN", reply, err)
	}

	// The right one gets the snapshot: both Query Groups, each with the
	// object its open Segment names and assigned to this replica, under the
	// activation the replica executes.
	stream, err := client.Connect(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(hello(identity.Token)); err != nil {
		t.Fatal(err)
	}
	reply, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reply.GetSnapshot()
	if snapshot == nil || snapshot.Chunks != 1 || len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot = %+v, want one chunk with the fixture's two Query Groups", reply)
	}
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Publication.SnapshotRevision != string(activation.Current.SnapshotRevision) || snapshot.Publication.ActivationRecordRevision != activation.RecordRevision {
		t.Fatalf("snapshot publication = %+v, want the activation %+v at record revision %d", snapshot.Publication, activation.Current, activation.RecordRevision)
	}
	for _, entry := range snapshot.Entries {
		if entry.Assignment == nil || entry.Assignment.DesiredWorkerId != fixture.cfg.PhaseTwo.Worker.ID || entry.Content == nil || entry.Content.ObjectDigest == "" {
			t.Fatalf("entry = %+v, want assigned to this replica with content", entry)
		}
		if entry.QueryGroup == string(fixture.queryGroup) && entry.Content.ObjectDigest != string(fixture.initialSchedule.Segment.ObjectDigest) {
			t.Fatalf("entry %s names %s, its open Segment names %s", entry.QueryGroup, entry.Content.ObjectDigest, fixture.initialSchedule.Segment.ObjectDigest)
		}
		// The view previews the record's timeline revision, and the record
		// says the timeline's own: the two the Worker compares are one
		// number from the day the Query Group is placed (decision-016
		// batch 4).
		timeline, err := fixture.repository.TimelineRecordRevision(ctx, execution.QueryGroupIdentity(entry.QueryGroup))
		if err != nil || timeline == 0 {
			t.Fatalf("timeline revision of %s = (%d, %v), want the activated timeline's", entry.QueryGroup, timeline, err)
		}
		if entry.Assignment.TimelineRecordRevision != timeline {
			t.Fatalf("entry %s previews timeline revision %d, the timeline is at %d", entry.QueryGroup, entry.Assignment.TimelineRecordRevision, timeline)
		}
		record, err := store.ReadAssignment(ctx, execution.QueryGroupIdentity(entry.QueryGroup))
		if err != nil || record.TimelineRecordRevision != timeline {
			t.Fatalf("record of %s says timeline revision %d (%v), the timeline is at %d", entry.QueryGroup, record.TimelineRecordRevision, err, timeline)
		}
	}
	// The receipt is counted for the one expected receiver.
	if err := stream.Send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Receipt{Receipt: &pb.Receipt{
		Incarnation: "test-incarnation", Version: snapshot.Version, Acked: true, Installed: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats = bundle.dependencies.ViewStreamStats()
		if stats.Counts.Installed == 1 && stats.Sessions == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v, want one session and the receipt installed", stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stats.Counts != (viewstream.Counts{Expected: 1, Sent: 1, Acked: 1, Installed: 1}) || stats.Counts.Switched != 0 {
		t.Fatalf("counts = %+v, want expected/sent/acked/installed 1 and switched 0 in the shadow", stats.Counts)
	}
	// Another round with nothing changed publishes no new revision.
	revision := stats.Revision
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if after := bundle.dependencies.ViewStreamStats(); after.Revision != revision || after.PublicationsSkipped == 0 {
		t.Fatalf("an unchanged round moved the revision %d -> %d (skipped=%d)", revision, after.Revision, after.PublicationsSkipped)
	}
}

// A stream that cannot publish leaves the round standing: the round
// returns nil and the failure is reported under the stream's own stage.
func TestAViewPublishFailureIsReportedAndTheRoundStands(t *testing.T) {
	var observed []observability.Observation
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observation)
		}),
	}}
	server, err := viewstream.NewServer(viewStreamAdmission{registry: nil, now: time.Now}, nil, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.dependencies.ViewStream = server
	runtime.dependencies.ViewSource = failingViewSource{err: errors.New("redis gone")}
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{
		QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "leader", OwnerEpoch: 4, LeaseToken: "token",
	}}
	runtime.publishView(context.Background(), authority, nil, nil)
	if len(observed) != 1 || observed[0].Stage != observability.StageViewPublished || observed[0].Result != observability.ResultDegraded ||
		observed[0].ViewStream == nil || observed[0].ViewStream.Event != "publish_failed" || !strings.Contains(observed[0].ViewStream.Reason, "redis gone") {
		t.Fatalf("observed = %+v, want one degraded view_published naming the failure", observed)
	}
	if stats := server.Stats(); stats.PublishFailures != 1 || stats.PublishFailureReason != viewstream.PublishFailureActivationUnreadable {
		t.Fatalf("server stats = %+v, want the failure recorded by its reason", stats)
	}
	// Not leading is a failure of the same kind, not a panic and not a stall.
	runtime.dependencies.ViewSource = staticViewSource{}
	runtime.publishView(context.Background(), authority, nil, nil)
	if len(observed) != 2 || !strings.Contains(observed[1].ViewStream.Reason, "not leading") {
		t.Fatalf("observed = %+v, want a second failure for not leading", observed)
	}
}

type failingViewSource struct{ err error }

func (source failingViewSource) LoadActivationHead(context.Context) (controlplane.ActivationState, error) {
	return controlplane.ActivationState{}, source.err
}

func (source failingViewSource) LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{}, source.err
}

func (source failingViewSource) ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error) {
	return nil, nil
}

func (source failingViewSource) DrainingContent(context.Context, execution.QueryGroupIdentity) (execution.ObjectDigest, []execution.OutputContextRef, bool, error) {
	return "", nil, false, source.err
}

type staticViewSource struct{}

func (staticViewSource) LoadActivationHead(context.Context) (controlplane.ActivationState, error) {
	return controlplane.ActivationState{RecordRevision: 1, Current: controlplane.SnapshotPublicationRef{SnapshotRevision: "snap", PublicationEpoch: 1}}, nil
}

func (staticViewSource) LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{}, nil
}

func (staticViewSource) ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error) {
	return nil, nil
}

func (staticViewSource) DrainingContent(context.Context, execution.QueryGroupIdentity) (execution.ObjectDigest, []execution.OutputContextRef, bool, error) {
	return "", nil, false, nil
}

// ApplyCutoverProgress passes the content through: this fixture has no
// cutover in progress.
func (_ failingViewSource) ApplyCutoverProgress(
	_ context.Context, _ controlplane.ActivationState, content map[execution.QueryGroupIdentity]controlplane.ContentEntry,
) (map[execution.QueryGroupIdentity]controlplane.ContentEntry, error) {
	return content, nil
}

// ApplyCutoverProgress passes the content through: this fixture has no
// cutover in progress.
func (_ staticViewSource) ApplyCutoverProgress(
	_ context.Context, _ controlplane.ActivationState, content map[execution.QueryGroupIdentity]controlplane.ContentEntry,
) (map[execution.QueryGroupIdentity]controlplane.ContentEntry, error) {
	return content, nil
}

// progressViewSource publishes new content for qg while its open Segment
// still runs old, as during a cutover in progress.
type progressViewSource struct{ staticViewSource }

func (progressViewSource) LoadActivationHead(context.Context) (controlplane.ActivationState, error) {
	return controlplane.ActivationState{RecordRevision: 2, Current: controlplane.SnapshotPublicationRef{SnapshotRevision: "snap", PublicationEpoch: 2},
		CutoverProgress: &controlplane.CutoverProgress{}}, nil
}

func (progressViewSource) LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{Groups: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {Digest: "new"}, "added": {Digest: "fresh"}}}, nil
}

func (progressViewSource) ApplyCutoverProgress(
	_ context.Context, state controlplane.ActivationState, content map[execution.QueryGroupIdentity]controlplane.ContentEntry,
) (map[execution.QueryGroupIdentity]controlplane.ContentEntry, error) {
	if state.CutoverProgress == nil {
		return content, nil
	}
	return map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {Digest: "old"}}, nil
}

// While a cutover is in progress the view gives each Query Group what its
// open Segment runs: a Query Group past the cursor keeps its old content,
// and one not added yet is not in the view. Given the manifest's, the first
// would stop on a scope mismatch and the second would be told to run what
// it has no timeline for.
func TestTheViewFollowsTheOpenSegmentsWhileACutoverIsInProgress(t *testing.T) {
	source := progressViewSource{}
	state, err := source.LoadActivationHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	running, err := runningViewContent(context.Background(), source, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running["qg"].Digest != "old" {
		t.Fatalf("view content = %+v, want only qg on the content its open Segment runs", running)
	}
}
