// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

func evidenceClient(t *testing.T, h *streamHarness) pb.ControlServiceClient {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///evidence-fixture", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return h.listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewControlServiceClient(conn)
}

func evidenceControlWorker(t *testing.T, h *streamHarness, client pb.ControlServiceClient) *workerStream {
	t.Helper()
	if err := h.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	if _, err := h.server.Publish(context.Background(), desiredAt(publicationA, map[string]string{"qg": "w1"},
		map[string]viewstream.Content{"qg": content("object", "strategy")})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := &workerStream{t: t, stream: stream, cancel: cancel}
	w.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{
		WorkerId: "w1", Incarnation: "incarnation-1", ProtocolVersion: viewstream.ProtocolVersion, StreamToken: "t1"}}})
	if snapshot := w.recvSnapshot(); snapshot.Version.Revision != 1 {
		t.Fatal("control snapshot did not arrive")
	}
	return w
}

func TestReadEvidenceUnconfiguredKeepsConnectCompatible(t *testing.T) {
	h := startServer(t)
	client := evidenceClient(t, h)
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unconfigured evidence code=%s", status.Code(err))
	}
	w := evidenceControlWorker(t, h, client)
	w.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if w.recv().GetHeartbeat() == nil {
		t.Fatal("unconfigured diagnostics changed Connect")
	}
}

func TestReadEvidenceValidatesWireBytesAndLeavesAuthorizationToHandler(t *testing.T) {
	h := startServer(t)
	client := evidenceClient(t, h)
	var calls atomic.Int32
	h.server.SetEvidenceHandler(func(_ context.Context, request *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
		calls.Add(1)
		if request.WorkerId != "" {
			return nil, status.Error(codes.PermissionDenied, "fixture authorization denied")
		}
		return &pb.EvidenceResult{ResponseJson: []byte(`{"status":"ok"}`)}, nil
	})
	// Request size includes all protobuf fields, not only params_json.
	request := &pb.EvidenceRequest{ParamsJson: make([]byte, viewstream.EvidenceRequestBytes-4)}
	if proto.Size(request) != viewstream.EvidenceRequestBytes {
		t.Fatal("invalid request boundary fixture")
	}
	if _, err := client.ReadEvidence(context.Background(), request); err != nil {
		t.Fatalf("boundary request: %s", status.Code(err))
	}
	request.Operation = "over-budget"
	if _, err := client.ReadEvidence(context.Background(), request); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("oversize request was accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("oversize request reached the handler")
	}
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{WorkerId: "runtime-must-authorize"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("handler authorization result was changed")
	}
	if _, err := h.server.ReadEvidence(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal("nil request accepted")
	}
	// A smaller bound for the fixture; the real one follows the channel's
	// response bound.
	defer func(previous int) { viewstream.EvidenceResponseBytes = previous }(viewstream.EvidenceResponseBytes)
	viewstream.EvidenceResponseBytes = 2 << 20
	response := &pb.EvidenceResult{ResponseJson: make([]byte, viewstream.EvidenceResponseBytes-4)}
	if proto.Size(response) != viewstream.EvidenceResponseBytes {
		t.Fatal("invalid response boundary fixture")
	}
	h.server.SetEvidenceHandler(func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error) { return response, nil })
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); err != nil {
		t.Fatalf("boundary response: %s", status.Code(err))
	}
	oversize := &pb.EvidenceResult{ResponseJson: make([]byte, viewstream.EvidenceResponseBytes)}
	h.server.SetEvidenceHandler(func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error) { return oversize, nil })
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("oversize response was accepted")
	}
	h.server.SetEvidenceHandler(func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error) { return nil, nil })
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); status.Code(err) != codes.Internal {
		t.Fatal("nil response was accepted")
	}
}

// Evidence reads between replicas are an operator's, a handful next to the
// business work, and have no count of their own: with eight handlers stalled
// (ignoring cancellation), a ninth request still reaches its handler.
func TestReadEvidenceHasNoCountOfItsOwn(t *testing.T) {
	h := startServer(t)
	client := evidenceClient(t, h)
	const stalled = 8
	entered := make(chan struct{}, stalled+1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.server.SetEvidenceHandler(func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
		entered <- struct{}{}
		<-release // Deliberately ignores cancellation.
		return &pb.EvidenceResult{}, nil
	})
	for i := 0; i < stalled; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := client.ReadEvidence(ctx, &pb.EvidenceRequest{}); done <- err }()
		select {
		case <-entered:
		case <-testwait.Expired(t):
			t.Fatalf("handler %d did not start", i+1)
		}
		cancel()
		select {
		case err := <-done:
			if status.Code(err) != codes.Canceled {
				t.Fatalf("cancel code=%s", status.Code(err))
			}
		case <-testwait.Expired(t):
			t.Fatal("canceled RPC did not return")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.ReadEvidence(ctx, &pb.EvidenceRequest{}); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("the ninth request was answered %s without reaching its handler", status.Code(err))
	case <-testwait.Expired(t):
		t.Fatal("the ninth request did not reach its handler")
	}
	cancel()
	<-done
	// A separate RPC on the very same connection must not occupy Connect's
	// locks or message queues, even with every evidence handler stalled.
	w := evidenceControlWorker(t, h, client)
	w.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if w.recv().GetHeartbeat() == nil {
		t.Fatal("stalled evidence blocked the control heartbeat")
	}
	// Rebinding is independent of both the running callbacks and Connect.
	h.server.SetEvidenceHandler(nil)
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatal("handler could not be disabled while requests were running")
	}
}

func TestReadEvidenceHonorsCallerAndServerDeadlines(t *testing.T) {
	h := startServer(t)
	client := evidenceClient(t, h)
	deadlines := make(chan time.Duration, 2)
	h.server.SetEvidenceHandler(func(ctx context.Context, _ *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadlines <- -1
		} else {
			deadlines <- time.Until(deadline)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond) // fixed-wait: the caller deadline the case expects to expire and finds inherited by the handler
	defer cancel()
	if _, err := client.ReadEvidence(ctx, &pb.EvidenceRequest{}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatal("caller deadline was not respected")
	}
	if remaining := <-deadlines; remaining <= 0 || remaining > 100*time.Millisecond {
		t.Fatal("handler did not inherit the shorter caller deadline")
	}
	started := time.Now()
	if _, err := client.ReadEvidence(context.Background(), &pb.EvidenceRequest{}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatal("server deadline was not enforced")
	}
	if elapsed := time.Since(started); elapsed < viewstream.EvidenceTimeout || elapsed > viewstream.EvidenceTimeout+2*time.Second {
		t.Fatalf("server timeout elapsed=%v", elapsed)
	}
	if remaining := <-deadlines; remaining <= 0 || remaining > viewstream.EvidenceTimeout {
		t.Fatal("handler did not inherit the server deadline")
	}
	// A caller with longer than EvidenceTimeout left keeps it: the hop does
	// not cut an operation that was given more.
	long, cancelLong := context.WithTimeout(context.Background(), 10*time.Second) // fixed-wait: a caller deadline longer than EvidenceTimeout, which the case finds the handler keeps
	defer cancelLong()
	go func() { _, _ = client.ReadEvidence(long, &pb.EvidenceRequest{}) }()
	if remaining := <-deadlines; remaining <= viewstream.EvidenceTimeout {
		t.Fatalf("handler got %v of a 10 s caller, want the caller's deadline", remaining)
	}
	cancelLong()
}
