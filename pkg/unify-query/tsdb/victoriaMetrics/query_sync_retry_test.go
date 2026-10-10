// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type vmRetryStep struct {
	size int
	err  error
}

type vmRetryCurl struct {
	steps       []vmRetryStep
	calls       []curl.Options
	cancel      context.CancelFunc
	cancelDelay time.Duration
}

func (c *vmRetryCurl) WithDecoder(func(context.Context, io.Reader, any) (int, error)) {}

func (c *vmRetryCurl) Request(_ context.Context, method string, opt curl.Options, _ any) (int, error) {
	if method != curl.Post {
		return 0, errors.New("unexpected method")
	}
	c.calls = append(c.calls, opt)
	step := c.steps[len(c.calls)-1]
	if c.cancel != nil && len(c.calls) == 1 {
		if c.cancelDelay > 0 {
			time.AfterFunc(c.cancelDelay, c.cancel)
		} else {
			c.cancel()
		}
	}
	return step.size, step.err
}

func TestVMQuerySyncRetry(t *testing.T) {
	mock.Init()
	http2Close := errors.New(http2ClientConnCloseError)
	http2GoAway := errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=1999, ErrCode=NO_ERROR, debug=""`)
	writeBrokenPipe := curl.HandleClientError(
		metadata.InitHashID(context.Background()), metadata.MsgHttpCurl, "http://example.invalid/query_sync/",
		&url.Error{Op: "Post", URL: "http://example.invalid/query_sync/", Err: &net.OpError{
			Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE},
		}},
	)
	readBrokenPipe := &net.OpError{Op: "read", Net: "tcp", Err: syscall.EPIPE}
	for _, tc := range []struct {
		name        string
		steps       []vmRetryStep
		cancel      bool
		cancelDelay time.Duration
		wantCalls   int
		wantErr     string
		wantErrIs   error
	}{
		{name: "recovers after partial body", steps: []vmRetryStep{{size: 51308, err: http2Close}, {size: 78888}}, wantCalls: 2},
		{name: "recovers after GOAWAY with full body", steps: []vmRetryStep{{size: 78888, err: http2GoAway}, {size: 78888}}, wantCalls: 2},
		{name: "recovers after wrapped network write EPIPE", steps: []vmRetryStep{{err: writeBrokenPipe}, {size: 78888}}, wantCalls: 2},
		{name: "second attempt fails again", steps: []vmRetryStep{{err: http2Close}, {err: http2Close}}, wantCalls: 2, wantErr: http2ClientConnCloseError},
		{name: "GOAWAY retry remains bounded", steps: []vmRetryStep{{err: http2GoAway}, {err: http2GoAway}}, wantCalls: 2, wantErr: "ErrCode=NO_ERROR"},
		{name: "write EPIPE retry remains bounded", steps: []vmRetryStep{{err: writeBrokenPipe}, {err: writeBrokenPipe}}, wantCalls: 2, wantErrIs: syscall.EPIPE},
		{name: "GOAWAY with server error is not retried", steps: []vmRetryStep{{err: errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=1999, ErrCode=ENHANCE_YOUR_CALM, debug=""`)}}, wantCalls: 1, wantErr: "ENHANCE_YOUR_CALM"},
		{name: "read EPIPE is not retried", steps: []vmRetryStep{{err: readBrokenPipe}}, wantCalls: 1, wantErrIs: syscall.EPIPE},
		{name: "string-only broken pipe is not retried", steps: []vmRetryStep{{err: errors.New("write tcp: write: broken pipe")}}, wantCalls: 1, wantErr: "broken pipe"},
		{name: "write connection reset is not retried", steps: []vmRetryStep{{err: &net.OpError{Op: "write", Net: "tcp", Err: syscall.ECONNRESET}}}, wantCalls: 1, wantErrIs: syscall.ECONNRESET},
		{name: "bare EPIPE is not retried", steps: []vmRetryStep{{err: syscall.EPIPE}}, wantCalls: 1, wantErrIs: syscall.EPIPE},
		{name: "ordinary error is not retried", steps: []vmRetryStep{{err: errors.New("backend unavailable")}}, wantCalls: 1, wantErr: "backend unavailable"},
		{name: "response limit is not retried", steps: []vmRetryStep{{err: &curl.ResponseBodyLimitError{Limit: 1024}}}, wantCalls: 1, wantErr: "response body exceeds"},
		{name: "canceled request is not retried", steps: []vmRetryStep{{err: http2Close}}, cancel: true, wantCalls: 1, wantErrIs: context.Canceled},
		{name: "canceled during retry delay", steps: []vmRetryStep{{err: http2Close}}, cancel: true, cancelDelay: 5 * time.Millisecond, wantCalls: 1, wantErrIs: context.Canceled},
		{name: "write EPIPE with canceled request is not retried", steps: []vmRetryStep{{err: writeBrokenPipe}}, cancel: true, wantCalls: 1, wantErrIs: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
			defer cancel()
			ctx = metadata.WithBackendResponseLimit(ctx, 1024)
			client := &vmRetryCurl{steps: tc.steps, cancelDelay: tc.cancelDelay}
			if tc.cancel {
				client.cancel = cancel
			}
			instance := &Instance{url: "http://example.invalid/query_sync/", timeout: time.Second, curl: client}
			ctx, span := trace.NewSpan(ctx, "vm-retry-test")
			err := instance.vmQuery(ctx, `{"query":"a"}`, &VmResponse{}, span)
			span.End(&err)
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, client.calls, tc.wantCalls)
			for index, opt := range client.calls {
				require.Equal(t, index+1, opt.Attempt)
				require.Equal(t, client.calls[0].UrlPath, opt.UrlPath)
				require.Equal(t, client.calls[0].Headers, opt.Headers)
				require.Equal(t, int64(1024), opt.MaxResponseBytes)
				require.Equal(t, client.calls[0].Body, opt.Body)
			}
		})
	}
}
