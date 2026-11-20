// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
)

var registerHealthOnce sync.Once

// startGrpcReceiver starts the receiver as the collector does, from its
// configuration, with only the gRPC server on and the health service as
// the service called.
func startGrpcReceiver(t *testing.T, maxRequestBytes int) *grpc.ClientConn {
	t.Helper()
	registerHealthOnce.Do(func() {
		RegisterRecvGrpcRoute(func(s *grpc.Server) {
			healthpb.RegisterHealthServer(s, health.NewServer())
		})
	})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := l.Addr().String()
	require.NoError(t, l.Close())

	content := fmt.Sprintf(`
  receiver:
    disabled: false
    grpc_server:
      enabled: true
      transport: "tcp"
      endpoint: "%s"
      middlewares:
        - "maxbytes;maxRequestBytes=%d"
`, endpoint, maxRequestBytes)
	r, err := New(confengine.MustLoadConfigContent(content))
	require.NoError(t, err)
	require.NoError(t, r.Start())
	t.Cleanup(func() { assert.NoError(t, r.Stop()) })

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func grpcRejected(t *testing.T) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	for _, reason := range []string{"too_large", "chunked_over_limit", "decompress", "decode", "token_partial", "no_pipeline"} {
		counts[reason], _ = metrictest.Value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": "grpc", "reason": reason})
	}
	return counts
}

// rawCodec sends the request bytes as they are, under the proto codec's
// name so the server decodes them as protobuf.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error)      { return v.([]byte), nil }
func (rawCodec) Unmarshal(data []byte, v any) error { *(v.(*[]byte)) = data; return nil }
func (rawCodec) Name() string                       { return "proto" }

// notGzip stands in for gzip on the client side only: it sends the message
// uncompressed while marking it gzip. The server decompresses with gzip.
type notGzip struct{ gzip encoding.Compressor }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (c notGzip) Compress(w io.Writer) (io.WriteCloser, error) { return nopWriteCloser{w}, nil }
func (c notGzip) Decompress(r io.Reader) (io.Reader, error)    { return c.gzip.Decompress(r) }
func (c notGzip) Name() string                                 { return "gzip" }

// A message over the receive limit, one marked compressed that does not
// decompress, and one that does not decode are refused by gRPC before the
// service sees them, and each is counted once under its own reason and no
// other. A call the service answers, with or without an error of its own,
// is not counted.
func TestGrpcServerCountsTheRequestsGrpcRefuses(t *testing.T) {
	conn := startGrpcReceiver(t, 1024)
	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// refused makes one call and checks that it moves the reason's cell by
	// one and no other cell. The server records the outcome after it
	// answers, so it is allowed a moment.
	refused := func(reason string, call func() error, code codes.Code) {
		t.Helper()
		want := grpcRejected(t)
		require.Equal(t, code, status.Code(call()), reason)
		want[reason]++
		assert.Eventually(t, func() bool { return assert.ObjectsAreEqual(want, grpcRejected(t)) }, 5*time.Second, 20*time.Millisecond, reason)
		assert.Equal(t, want, grpcRejected(t), reason)
	}

	before := grpcRejected(t)
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{Service: "not-registered"})
	require.Equal(t, codes.NotFound, status.Code(err))

	refused("too_large", func() error {
		_, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: strings.Repeat("s", 2048)})
		return err
	}, codes.ResourceExhausted)

	refused("decode", func() error {
		var out []byte
		return conn.Invoke(ctx, "/grpc.health.v1.Health/Check", []byte{0xff, 0xff, 0xff, 0xff}, &out, grpc.ForceCodec(rawCodec{}))
	}, codes.Internal)

	refused("decompress", func() error {
		gzip := encoding.GetCompressor("gzip")
		require.NotNil(t, gzip)
		encoding.RegisterCompressor(notGzip{gzip: gzip})
		defer encoding.RegisterCompressor(gzip)
		_, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: "compressed"}, grpc.UseCompressor("gzip"))
		return err
	}, codes.Internal)

	// The two answered calls, made first, left nothing behind them.
	want := map[string]float64{}
	for reason, v := range before {
		want[reason] = v
	}
	want["too_large"]++
	want["decode"]++
	want["decompress"]++
	assert.Equal(t, want, grpcRejected(t))
}
