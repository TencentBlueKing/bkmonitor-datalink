// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Execute the actual SQL predicates so the fixture cannot conceal backend boundary loss.
func influxBoundaryHTTP(t *testing.T, sampleTimes []int64, sourceInfo bool, infoCalls *atomic.Int32) http.HandlerFunc {
	t.Helper()
	pattern := regexp.MustCompile(`time (>=|>) ([0-9]+) and time (<=|<) ([0-9]+)`)
	return func(w http.ResponseWriter, r *http.Request) {
		sql := r.URL.Query().Get("q")
		match := pattern.FindStringSubmatch(sql)
		if len(match) != 5 {
			t.Errorf("unexpected time predicate: %s", sql)
			http.Error(w, "unexpected time predicate", http.StatusBadRequest)
			return
		}
		start, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			t.Error(err)
			return
		}
		end, err := strconv.ParseInt(match[4], 10, 64)
		if err != nil {
			t.Error(err)
			return
		}
		times := sampleTimes
		tags := map[string]string{"source_id": "a", "middle_id": "b"}
		if strings.Contains(sql, "source_info_relation") {
			infoCalls.Add(1)
			tags = map[string]string{"source_id": "a", "zone": "east"}
		} else if sourceInfo {
			// Keep the edge alive on every grid point; only source-info controls root presence.
			times = []int64{1700000100000, 1700000160000, 1700000220000}
		}
		var values [][]any
		for _, timestamp := range times {
			ns := timestamp * int64(time.Millisecond)
			afterStart := ns > start || (ns == start && match[1] == ">=")
			beforeEnd := ns < end || (ns == end && match[3] == "<=")
			if afterStart && beforeEnd {
				values = append(values, []any{time.UnixMilli(timestamp).UTC().Format(time.RFC3339Nano), 1})
			}
		}
		var series []any
		if len(values) > 0 {
			series = []any{map[string]any{"name": "relation", "tags": tags, "columns": []string{"_time", "_value"}, "values": values}}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"series": series}}}); err != nil {
			t.Error(err)
		}
	}
}

func TestTimeGraphInfluxWindowBoundaries(t *testing.T) {
	const at int64 = 1700000100000
	for _, mode := range []string{"instant", "range"} {
		for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
			for _, sourceInfo := range []bool{false, true} {
				// A range exercises the first fetch boundary and the final evaluation timestamp.
				offsets := []int64{-60000, -59999, 0, 1}
				if mode == "range" {
					offsets = append(offsets, 60000, 120000, 120001)
				}
				for _, offset := range offsets {
					name := fmt.Sprintf("%s/%s/source_info_%t/offset_%d", mode, format, sourceInfo, offset)
					t.Run(name, func(t *testing.T) {
						sample := at + offset
						infoCalls := &atomic.Int32{}
						setupTimeGraphInfluxDeadline(t, influxBoundaryHTTP(t, []int64{sample}, sourceInfo, infoCalls))
						previousEngine := localprom.GlobalEngine
						localprom.GlobalEngine = pl.NewEngine(pl.EngineOpts{Timeout: time.Second, MaxSamples: 10000})
						t.Cleanup(func() { localprom.GlobalEngine = previousEngine })
						provider := sharedTopologyQueryProvider().(contractSchemaProvider)
						provider.schemas = provider.schemas[:1]
						provider.fields["source"] = append(provider.fields["source"], "zone")
						model := &Model{schemaProvider: provider}
						model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
							if err := q.ToTime(ctx); err != nil {
								return nil, err
							}
							reference := metadata.QueryReference{}
							for _, query := range q.QueryList {
								physical := timeGraphInfluxPhysicalQuery(query.FieldName)
								// Keep the actual local Engine and raw InfluxDB adapter in the path.
								physical.Aggregates = nil
								physical.MetricNames = []string{query.ReferenceName}
								reference[query.ReferenceName] = append(reference[query.ReferenceName], &metadata.QueryMetric{ReferenceName: query.ReferenceName, MetricName: query.FieldName, QueryList: metadata.QueryList{physical}})
							}
							metadata.SetQueryReference(ctx, reference)
							return reference, nil
						}
						request := cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: at / 1000, LookBackDelta: "1m", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: 1, ResponseFormat: format}
						frames := []int64{at}
						if mode == "range" {
							request.Timestamp, request.StartTime, request.EndTime, request.Step = 0, at/1000, (at+120000)/1000, "1m"
							frames = []int64{at, at + 60000, at + 120000}
						}
						if sourceInfo {
							request.SourceExpandInfo = cmdb.Matcher{"zone": "east"}
						}
						result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
						require.NoError(t, err)
						snapshots := result.Snapshots
						if format == cmdb.CompactTopologyFormat {
							snapshots = decodeCompactForTest(t, result.Compact)
						}
						require.Len(t, snapshots, len(frames))
						if sourceInfo {
							require.Positive(t, infoCalls.Load(), "source-info must reach the real HTTP backend")
						}
						for n, frame := range frames {
							snapshot := snapshots[n]
							require.Equal(t, frame, snapshot.Timestamp)
							require.False(t, snapshot.Partial)
							require.Empty(t, snapshot.PartialReason)
							if sample > frame-60000 && sample <= frame {
								require.Len(t, snapshot.Nodes, 2, "frame %d must retain the active boundary sample", n)
								require.Len(t, snapshot.Edges, 1)
							} else {
								require.Empty(t, snapshot.Nodes, "frame %d must exclude out-of-window samples", n)
								require.Empty(t, snapshot.Edges)
							}
						}
					})
				}
			}
		}
	}
}
