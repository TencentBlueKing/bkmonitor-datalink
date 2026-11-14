// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	prom "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var updateDecodeGolden = flag.Bool("update-decode-golden", false, "rewrite testdata/decode_tables.golden.json")

func decodeLabels() labels.Labels {
	return labels.Labels{
		{Name: "__bk_33___ext__bk_47__x", Value: "a"},
		{Name: "__ext__bk_46__cloud__bk_46__tencent__bk_46__com__bk_47__asset__bk_45__code", Value: "v"},
		{Name: "__bk_20013____bk_25991__", Value: "中文"},
		{Name: "__bk_99999999999999999999__", Value: "overflow"},
		{Name: "__bk_x__", Value: "x"},
		{Name: "__bk___", Value: " "},
		{Name: "a__bk_46__b__bk_46__c", Value: "repeated"},
		{Name: "container", Value: "plain"},
	}
}

func decodeTables() []*Table {
	decode := new(metadata.FieldFormat).DecodeFunc()
	matrix := prom.Matrix{
		{
			Metric: decodeLabels(),
			Points: []prom.Point{{T: 1000, V: 1}, {T: 2000, V: math.NaN()}, {T: 3000, V: math.Inf(1)}, {T: 4000, V: 2.5}},
		},
		{Metric: labels.Labels{{Name: "__bk_46__", Value: "dot"}}, Points: []prom.Point{{T: 1000, V: 0}}},
	}
	vector := prom.Vector{
		{Point: prom.Point{T: 5000, V: 3}, Metric: decodeLabels()},
		{Point: prom.Point{T: 5000, V: -1}, Metric: labels.Labels{{Name: "__bk_95__bk_46__", Value: "no rescan"}}},
	}
	var tables []*Table
	for i, series := range matrix {
		tables = append(tables, NewTable(i, series, decode))
	}
	for i, sample := range vector {
		tables = append(tables, NewTableWithSample(i, sample, decode))
	}
	return tables
}

// Matrix and Vector tables with decoded label names; the golden file is the output of the
// code before label decoding compiled its pattern once, and must stay byte-identical.
func TestNewTableDecodeGolden(t *testing.T) {
	got, err := json.MarshalIndent(decodeTables(), "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join("testdata", "decode_tables.golden.json")
	if *updateDecodeGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

var tableSink *Table

func BenchmarkNewTableDecode(b *testing.B) {
	decode := new(metadata.FieldFormat).DecodeFunc()
	series := prom.Series{Metric: decodeLabels()}
	for i := 0; i < 60; i++ {
		series.Points = append(series.Points, prom.Point{T: int64(i) * 60000, V: float64(i)})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tableSink = NewTable(i, series, decode)
	}
}
