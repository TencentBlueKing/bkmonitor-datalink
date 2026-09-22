// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestExactTimeGridCacheCases(t *testing.T) {
	for _, tt := range []struct {
		name        string
		start, step int64
		exact       bool
		want        int
	}{
		{"精确一分钟网格", 1700000001, 60, true, 1},
		{"精确短步长网格", 1700000001, 10, true, 1},
		{"精确对齐网格", 1700000040, 60, true, 1},
		{"旧短步长行为", 1700000001, 60, false, 0},
		{"旧长步长行为", 1700000001, 120, false, 1},
		{"零步长不除零", 1700000001, 0, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.exact {
				ctx = metadata.WithExactTimeGrid(ctx)
			}
			require.Equal(t, tt.want, instance.noCache(ctx, tt.start, tt.step))
		})
	}
}
