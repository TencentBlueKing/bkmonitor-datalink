package trace

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type receivedTrace struct {
	Header  string
	Request *collectortrace.ExportTraceServiceRequest
}
type traceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	output chan receivedTrace
}

func (r *traceReceiver) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.output <- receivedTrace{Header: md.Get("authorization")[0], Request: req}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func TestOTelCredentialProtocols(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		for _, enabled := range []bool{false, true} {
			name := protocol + "-legacy"
			if enabled {
				name = protocol + "-kms"
			}
			t.Run(name, func(t *testing.T) {
				viper.Reset()
				oldHost, oldPort, oldToken, oldName := otlpHost, otlpPort, otlpToken, ServiceName
				t.Cleanup(func() {
					viper.Reset()
					otlpHost, otlpPort, otlpToken, ServiceName = oldHost, oldPort, oldToken, oldName
				})
				t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=env-secret")
				t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=traces-env-secret")
				viper.Set("kms.enabled", enabled)
				viper.Set("trace.otlp.headers", map[string]string{"authorization": "kms-header-secret"})
				otlpToken, ServiceName = "trace-token-secret", "test-uq"
				output := make(chan receivedTrace, 1)
				if protocol == "http" {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						data, err := io.ReadAll(req.Body)
						if err != nil {
							http.Error(w, "read failed", 500)
							return
						}
						body := new(collectortrace.ExportTraceServiceRequest)
						if proto.Unmarshal(data, body) != nil {
							http.Error(w, "decode failed", 500)
							return
						}
						output <- receivedTrace{Header: req.Header.Get("Authorization"), Request: body}
						w.Header().Set("Content-Type", "application/x-protobuf")
						w.WriteHeader(200)
					}))
					defer server.Close()
					var err error
					otlpHost, otlpPort, err = net.SplitHostPort(server.Listener.Addr().String())
					require.NoError(t, err)
				} else {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					otlpHost, otlpPort, err = net.SplitHostPort(listener.Addr().String())
					require.NoError(t, err)
					server := grpc.NewServer()
					collectortrace.RegisterTraceServiceServer(server, &traceReceiver{output: output})
					go func() { _ = server.Serve(listener) }()
					defer server.Stop()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				s := new(Service)
				client := s.newHTTPClient()
				if protocol == "grpc" {
					client = s.newGrpcClient()
				}
				exporter, err := otlptrace.New(ctx, client)
				require.NoError(t, err)
				provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithResource(s.newResource()))
				_, span := provider.Tracer("test").Start(ctx, "test-span")
				span.End()
				select {
				case result := <-output:
					want := "traces-env-secret"
					if enabled {
						want = "kms-header-secret"
					}
					require.Equal(t, want, result.Header)
					var token string
					for _, attr := range result.Request.ResourceSpans[0].Resource.Attributes {
						if attr.Key == "bk.data.token" {
							token = attr.Value.GetStringValue()
						}
					}
					require.Equal(t, "trace-token-secret", token, "existing legitimate telemetry authentication must remain")
				case <-ctx.Done():
					t.Fatal("exporter did not send the authenticated request")
				}
				require.NoError(t, provider.Shutdown(ctx))
			})
		}
	}
}
