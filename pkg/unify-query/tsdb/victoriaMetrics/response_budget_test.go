// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type responseBudgetCurl struct{ limit int64 }

func (c *responseBudgetCurl) WithDecoder(func(context.Context, io.Reader, any) (int, error)) {}

func (c *responseBudgetCurl) Request(_ context.Context, _ string, opt curl.Options, _ any) (int, error) {
	c.limit = opt.MaxResponseBytes
	return 0, nil
}

func TestVMRequestPropagatesResponseBudget(t *testing.T) {
	mock.Init()
	for _, limit := range []int64{0, 4096} {
		ctx := metadata.InitHashID(context.Background())
		if limit > 0 {
			ctx = metadata.WithBackendResponseLimit(ctx, limit)
		}
		client := &responseBudgetCurl{}
		instance := &Instance{url: "http://example.invalid", timeout: time.Second, curl: client}
		ctx, span := trace.NewSpan(ctx, "budget-test")
		err := instance.vmQuery(ctx, "{}", &VmResponse{}, span)
		span.End(&err)
		require.NoError(t, err)
		require.Equal(t, limit, client.limit)
	}
}

// An instant and a range query get the same rejection from the gateway, and callers
// that split an oversized read rely on recognising it either way.
func TestFormatsClassifyBackendResponseSizeLimit(t *testing.T) {
	mock.Init()
	formats := map[string]func(context.Context, *VmResponse, *trace.Span) error{
		"range": func(ctx context.Context, resp *VmResponse, span *trace.Span) error {
			_, _, err := (&Instance{}).matrixFormat(ctx, resp, span)
			return err
		},
		"instant": func(ctx context.Context, resp *VmResponse, span *trace.Span) error {
			_, err := (&Instance{}).vectorFormat(ctx, resp, span)
			return err
		},
	}
	for name, format := range formats {
		t.Run(name, func(t *testing.T) {
			run := func(response *VmResponse) error {
				ctx := metadata.InitHashID(context.Background())
				ctx, span := trace.NewSpan(ctx, "response-size-limit-test")
				err := format(ctx, response, span)
				span.End(&err)
				return err
			}

			// The gateway's usual shape puts a generic message in message and the limit and
			// estimate in errors.error; either field carries the rejection. Each field appears
			// once: the message is the outer context, errors.error the backend's detail.
			for _, shape := range []struct{ name, message, detail string }{
				{"detail_in_errors", "返回结果集大小超过最大值", "Response body size exceeds 500 MB, estimatedSize: 550 MB"},
				{"detail_in_message", "Response body size exceeds 100 MB, estimatedSize: 135 MB", ""},
				{"lowercase_detail", "", "response body size exceeds backend limit"},
			} {
				t.Run(shape.name, func(t *testing.T) {
					response := &VmResponse{Message: shape.message, Code: "400"}
					response.Errors.Error = shape.detail
					err := run(response)
					require.ErrorIs(t, err, metadata.ErrBackendResponseTooLarge)
					for _, field := range []string{shape.message, shape.detail} {
						if field != "" {
							require.Equal(t, 1, strings.Count(err.Error(), field), err.Error())
						}
					}
				})
			}

			err := run(&VmResponse{Message: "query timed out", Code: "400"})
			require.Error(t, err)
			require.NotErrorIs(t, err, metadata.ErrBackendResponseTooLarge)
		})
	}
}
