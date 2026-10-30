// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func serveRetryHTTP2Connection(conn net.Conn, goAway bool, body string) error {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("unexpected HTTP/2 preface: %q", preface)
	}
	frames := http2.NewFramer(conn, conn)
	if err := frames.WriteSettings(); err != nil {
		return err
	}
	for {
		frame, err := frames.ReadFrame()
		if err != nil {
			return err
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := frames.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.HeadersFrame:
			if goAway {
				// 标记该请求已被处理，避免 HTTP/2 客户端自行重试未处理的流。
				return frames.WriteGoAway(frame.StreamID, http2.ErrCodeNo, nil)
			}
			var block bytes.Buffer
			encoder := hpack.NewEncoder(&block)
			if err := encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"}); err != nil {
				return err
			}
			if err := frames.WriteHeaders(http2.HeadersFrameParam{
				StreamID: frame.StreamID, BlockFragment: block.Bytes(), EndHeaders: true,
			}); err != nil {
				return err
			}
			return frames.WriteData(frame.StreamID, true, []byte(body))
		}
	}
}

func TestVMQueryRealHTTP2RetryOutcomes(t *testing.T) {
	for _, outcome := range []string{"recovered", "failed", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			var connections atomic.Int32
			served := make(chan error, 1)
			go func() {
				for attempt := 1; attempt <= 2; attempt++ {
					conn, err := listener.Accept()
					if err != nil {
						served <- err
						return
					}
					connections.Add(1)
					body := "{}"
					if outcome == "canceled" {
						body = "{"
					}
					if err := serveRetryHTTP2Connection(conn, attempt == 1 || outcome == "failed", body); err != nil {
						served <- err
						return
					}
				}
				served <- nil
			}()
			t.Cleanup(func() {
				_ = listener.Close()
				if err := <-served; err != nil && !errors.Is(err, net.ErrClosed) {
					t.Error(err)
				}
			})

			previousTransport := http.DefaultTransport
			transport := &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			}
			http.DefaultTransport = transport
			t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previousProvider := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previousProvider)
				require.NoError(t, provider.Shutdown(context.Background()))
			})

			client := &curl.HttpCurl{}
			if outcome == "canceled" {
				client.WithDecoder(func(_ context.Context, reader io.Reader, result any) (int, error) {
					body, err := io.ReadAll(reader)
					if err != nil {
						return len(body), err
					}
					// 第二次真实响应已读完；解析期间取消，实际 JSON 错误不能覆盖取消原因。
					cancel()
					return len(body), json.Unmarshal(body, result)
				})
			}
			instance := &Instance{url: "http://" + listener.Addr().String(), timeout: time.Second, curl: client}
			queryCtx, span := trace.NewSpan(metadata.InitHashID(ctx), "real-http2-retry")
			err = instance.vmQuery(queryCtx, "{}", &VmResponse{}, span)
			span.End(&err)
			switch outcome {
			case "recovered":
				require.NoError(t, err)
			case "failed":
				require.ErrorContains(t, err, "GOAWAY")
			case "canceled":
				require.ErrorIs(t, err, context.Canceled)
			}
			require.Equal(t, int32(2), connections.Load())
			var recorded string
			for _, ended := range recorder.Ended() {
				if ended.Name() == "real-http2-retry" {
					for _, attr := range ended.Attributes() {
						if string(attr.Key) == "query-sync-retry.outcome" {
							recorded = attr.Value.AsString()
						}
					}
				}
			}
			require.Equal(t, outcome, recorded)
		})
	}
}
