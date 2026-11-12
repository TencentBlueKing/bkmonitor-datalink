// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"strconv"
	"strings"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// frontierProbeForTest 表示宽读取前的计数：在关系读取的聚合之外再计数一次。
func frontierProbeForTest(ts *structured.QueryTs) bool {
	return ts != nil && len(ts.QueryList) == 1 && len(ts.QueryList[0].AggregateMethodList) == 2
}

// frontierProbeAnswer 与存储一样回答计数：按第一级聚合的维度对 matched 中的序列去重，
// 返回一条序列，取值为去重后的序列数，时间为计数的评估点。
func frontierProbeAnswer(ts *structured.QueryTs, matched pl.Matrix) pl.Matrix {
	dims := ts.QueryList[0].AggregateMethodList[0].Dimensions
	seen := map[string]struct{}{}
	for _, series := range matched {
		values := make([]string, len(dims))
		for i, dim := range dims {
			values[i] = series.Metric.Get(dim)
		}
		seen[strings.Join(values, "\x00")] = struct{}{}
	}
	at, _ := strconv.ParseInt(ts.End, 10, 64)
	return pl.Matrix{{Points: []pl.Point{{T: at * 1000, V: float64(len(seen))}}}}
}
