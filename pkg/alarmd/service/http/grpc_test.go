// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package httpservice

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

type admitEveryone struct{}

func (admitEveryone) Admit(context.Context, string, string) (string, error) { return "", nil }

// The control stream shares the query listener: gRPC over HTTP/2 in the
// clear, routed by content type, with plain HTTP untouched beside it. Until
// the runtime installs the stream a gRPC call is refused UNAVAILABLE, the
// way the API answers 503, and never mistaken for a listener without one.
func TestTheQueryListenerServesTheControlStreamOverH2C(t *testing.T) {
	queryAddress := reserveAddress(t)
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(""))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrors := make(chan error, 1)
	go func() { runErrors <- server.Run(ctx, queryAddress, time.Second) }()
	waitForStatus(t, "http://"+queryAddress+"/healthz", http.StatusOK)

	conn, err := grpc.NewClient(queryAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewControlServiceClient(conn)
	callCtx, cancelCall := context.WithTimeout(ctx, 5*time.Second)
	defer cancelCall()

	// Before the stream is installed: UNAVAILABLE, in gRPC's own words.
	early, err := client.Connect(callCtx)
	if err == nil {
		_ = early.Send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{WorkerId: "w1", Incarnation: "i", ProtocolVersion: 1}}})
		_, err = early.Recv()
	}
	if code := status.Code(err); code != codes.Unavailable {
		t.Fatalf("before the stream is installed: code=%s err=%v, want Unavailable", code, err)
	}

	// Installed: a Hello is answered by the stream, and plain HTTP still works.
	stream, err := viewstream.NewServer(admitEveryone{}, nil, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Lead(3); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Publish(ctx, viewstream.Desired{ControlEpoch: 3}); err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterControlServiceServer(grpcServer, stream)
	server.SetGRPC(grpcServer)
	connected, err := client.Connect(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := connected.Send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Hello{Hello: &pb.Hello{WorkerId: "w1", Incarnation: "i", ProtocolVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	reply, err := connected.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := reply.GetSnapshot(); snapshot == nil || snapshot.Version.ControlEpoch != 3 || snapshot.Version.Revision != 1 {
		t.Fatalf("reply over the query listener = %+v, want the term's snapshot", reply)
	}
	if status := get(t, "http://"+queryAddress+"/healthz"); status != http.StatusOK {
		t.Fatalf("healthz beside the stream = %d", status)
	}
	// The stream survives longer than every request timeout the listener
	// sets: nothing but ReadHeaderTimeout is set, and it does not apply to
	// an open stream.
	time.Sleep(50 * time.Millisecond)
	if err := connected.Send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{SentAtMs: 1}}}); err != nil {
		t.Fatal(err)
	}
	if beat, err := connected.Recv(); err != nil || beat.GetHeartbeat() == nil {
		t.Fatalf("heartbeat over the listener: %+v %v", beat, err)
	}
	cancel()
	if err := <-runErrors; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
}
