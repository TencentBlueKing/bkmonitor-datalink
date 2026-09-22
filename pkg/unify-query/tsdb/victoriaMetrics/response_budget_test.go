// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"io"
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
