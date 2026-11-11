package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	uqjson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

func TestLargeTopologySnapshotEncodingPreservesResponse(t *testing.T) {
	item := cmdb.SharedTopologyResponseData{
		Code: http.StatusOK, StartTime: 1700000000, EndTime: 1700000001,
		Step: "1s", PointCount: 2, Message: "中文 <&>\u2028",
		Snapshots: []cmdb.SharedTopologySnapshot{
			{Timestamp: 1700000000, Nodes: []cmdb.SharedTopologyNode{{ID: ^uint64(0), ResourceType: "node", Dimensions: cmdb.Matcher{"name": "节点<&>", "key": "value"}}}, Edges: []cmdb.SharedTopologyEdge{}},
			{Timestamp: 1700000001, Nodes: []cmdb.SharedTopologyNode{}, Edges: []cmdb.SharedTopologyEdge{}, Partial: true, PartialReason: "样本缺失"},
		},
	}
	wantItem, err := stdjson.Marshal(item)
	require.NoError(t, err)
	wantConfiguredItem, err := uqjson.Marshal(item)
	require.NoError(t, err)
	require.Equal(t, wantConfiguredItem, wantItem)
	chunks, size, err := marshalLargeTopologySnapshots(item)
	require.NoError(t, err)
	require.Equal(t, len(wantItem), size)
	require.Equal(t, wantItem, bytes.Join(chunks, nil))

	response := &topologyResponse{
		TraceID: "0123456789abcdef0123456789abcdef",
		Data:    []stdjson.RawMessage{wantItem, nil, wantItem},
		chunks:  [][][]byte{nil, chunks, nil},
	}
	want, err := stdjson.Marshal(struct {
		TraceID string               `json:"trace_id"`
		Data    []stdjson.RawMessage `json:"data"`
	}{response.TraceID, []stdjson.RawMessage{wantItem, wantItem, wantItem}})
	require.NoError(t, err)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/topology", nil)
	proxy.TraceJSONEncoding(ctx)
	proxy.WriteJSON(ctx.Request.Context(), ctx, http.StatusOK, response)
	require.NoError(t, proxy.ResponseWriteError(ctx))
	require.Equal(t, want, writer.Body.Bytes())
	require.Equal(t, "application/json; charset=utf-8", writer.Header().Get("Content-Type"))

	var decoded cmdb.SharedTopologyResponse
	require.NoError(t, stdjson.Unmarshal(writer.Body.Bytes(), &decoded))
	require.Len(t, decoded.Data, 3)
	require.Equal(t, item.Snapshots, decoded.Data[1].Snapshots)
}

func TestBoundedTopologyEncodingPreservesStringsAndUint64(t *testing.T) {
	for _, label := range []string{"", "中文<&>\u2028\u2029", "quote\"\\\n", string([]byte{0xff, 'x'})} {
		t.Run(fmt.Sprintf("%x", label), func(t *testing.T) {
			ids := []uint64{2, 10, 9007199254740993, math.MaxUint64}
			dimensions := []cmdb.Matcher{nil, {}, {"name": label, string([]byte{'k', 0xff}): label}, {"id": "max", "empty": ""}}
			snapshot := cmdb.SharedTopologySnapshot{Timestamp: 1700000000000, Partial: true, PartialReason: label}
			compact := &cmdb.CompactTopology{Version: 1, Timestamps: []int64{snapshot.Timestamp}, Partial: []cmdb.CompactTopologyPartial{}}
			for i, id := range ids {
				snapshot.Nodes = append(snapshot.Nodes, cmdb.SharedTopologyNode{ID: id, ResourceType: cmdb.Resource(label), Dimensions: dimensions[i]})
				snapshot.Edges = append(snapshot.Edges, cmdb.SharedTopologyEdge{Source: id, Target: math.MaxUint64, RelationType: label, MetricName: label, Category: label, Direction: label})
				compact.Nodes = append(compact.Nodes, cmdb.CompactTopologyNode{ID: fmt.Sprint(id), ResourceType: cmdb.Resource(label), Dimensions: dimensions[i], Mask: []string{"0000000000000001"}})
				compact.Edges = append(compact.Edges, cmdb.CompactTopologyEdge{Source: fmt.Sprint(id), Target: fmt.Sprint(uint64(math.MaxUint64)), RelationType: label, MetricName: label, Category: label, Direction: label, Mask: []string{"0000000000000001"}})
			}
			for _, payload := range []any{
				cmdb.SharedTopologyResponseData{Code: http.StatusOK, Message: label, Snapshots: []cmdb.SharedTopologySnapshot{snapshot}},
				cmdb.CompactTopologyResponseData{Code: http.StatusOK, Message: label, ResponseFormat: cmdb.CompactTopologyFormat, Compact: compact},
			} {
				want, err := stdjson.Marshal(payload)
				require.NoError(t, err)
				// The default bound selects whole-item encoding; a zero bound forces
				// compact's per-row encoding. Both produce the same bytes.
				for _, wholeItemBytes := range []int64{topologyCompactWholeItemBytes, 0} {
					previous := topologyCompactWholeItemBytes
					topologyCompactWholeItemBytes = wholeItemBytes
					chunks, size, err := marshalTopologyItem(context.Background(), payload)
					topologyCompactWholeItemBytes = previous
					require.NoError(t, err)
					encoded := bytes.Join(chunks, nil)
					require.Equal(t, len(want), size)
					require.Equal(t, want, encoded)
					require.True(t, utf8.Valid(encoded))
				}
			}
		})
	}
}

type topologyShortWriter struct {
	header http.Header
	calls  int
}

func (w *topologyShortWriter) Header() http.Header         { return w.header }
func (w *topologyShortWriter) WriteHeader(int)             {}
func (w *topologyShortWriter) Write(p []byte) (int, error) { w.calls++; return len(p) / 2, nil }

func TestBoundedTopologyMixedResponseAndShortWrite(t *testing.T) {
	item := largeTopologyBenchmarkItem()
	parts, _, err := marshalLargeTopologySnapshots(item)
	require.NoError(t, err)
	snap, err := stdjson.Marshal(item)
	require.NoError(t, err)
	compact := stdjson.RawMessage(`{"code":200,"response_format":"compact-v1","compact":null}`)
	failed := stdjson.RawMessage(`{"code":400,"start_time":0,"end_time":0,"step":"","point_count":0,"snapshots":[],"message":"bad"}`)
	response := &topologyResponse{TraceID: "0123456789abcdef0123456789abcdef", Data: []stdjson.RawMessage{nil, compact, failed}, chunks: [][][]byte{parts, nil, nil}}
	want, err := stdjson.Marshal(struct {
		TraceID string               `json:"trace_id"`
		Data    []stdjson.RawMessage `json:"data"`
	}{response.TraceID, []stdjson.RawMessage{snap, compact, failed}})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(rec)
	g.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	proxy.TraceJSONEncoding(g)
	proxy.WriteJSON(g.Request.Context(), g, http.StatusOK, response)
	require.NoError(t, proxy.ResponseWriteError(g))
	require.Equal(t, want, rec.Body.Bytes())
	w := &topologyShortWriter{header: http.Header{}}
	g, _ = gin.CreateTestContext(w)
	g.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	proxy.TraceJSONEncoding(g)
	proxy.WriteJSON(g.Request.Context(), g, http.StatusOK, response)
	require.ErrorIs(t, proxy.ResponseWriteError(g), io.ErrShortWrite)
	require.Equal(t, 1, w.calls)
}

func TestLargeTopologySnapshotEncodingEmptyAndFailed(t *testing.T) {
	for _, item := range []cmdb.SharedTopologyResponseData{
		{Code: http.StatusOK, Step: "1s", Snapshots: []cmdb.SharedTopologySnapshot{}},
		{Code: http.StatusBadRequest, Message: "查询失败", Snapshots: []cmdb.SharedTopologySnapshot{}},
	} {
		want, err := stdjson.Marshal(item)
		require.NoError(t, err)
		chunks, size, err := marshalLargeTopologySnapshots(item)
		require.NoError(t, err)
		require.Equal(t, len(want), size)
		require.Equal(t, want, bytes.Join(chunks, nil))
	}
}

func TestTopologyResponseSegmentsPreserveEmptyRawMessage(t *testing.T) {
	response := &topologyResponse{
		Data:   []stdjson.RawMessage{nil, stdjson.RawMessage(`{"code":200}`)},
		chunks: make([][][]byte, 2),
	}
	parts, err := response.JSONSegments()
	require.NoError(t, err)
	require.Equal(t, `{"trace_id":"","data":[null,{"code":200}]}`, string(bytes.Join(parts, nil)))

	response.chunks = response.chunks[:1]
	_, err = response.JSONSegments()
	require.ErrorContains(t, err, "chunk lists")
}

func BenchmarkLargeTopologySnapshotEncoding(b *testing.B) {
	item := largeTopologyBenchmarkItem()
	for _, mode := range []string{"marshal", "segments"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "marshal" {
					encoded, err := stdjson.Marshal(item)
					if err != nil {
						b.Fatal(err)
					}
					b.ReportMetric(float64(len(encoded)), "wire-bytes")
				} else {
					_, size, err := marshalLargeTopologySnapshots(item)
					if err != nil {
						b.Fatal(err)
					}
					b.ReportMetric(float64(size), "wire-bytes")
				}
			}
		})
	}
}

func BenchmarkLargeTopologyResponsePreparation(b *testing.B) {
	item := largeTopologyBenchmarkItem()
	for _, mode := range []string{"marshal", "segments"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "marshal" {
					encoded, err := uqjson.Marshal(item)
					if err != nil {
						b.Fatal(err)
					}
					body, err := uqjson.Marshal(&topologyResponse{TraceID: "abc", Data: []stdjson.RawMessage{encoded}})
					if err != nil {
						b.Fatal(err)
					}
					b.ReportMetric(float64(len(body)), "wire-bytes")
				} else {
					chunks, _, err := marshalLargeTopologySnapshots(item)
					if err != nil {
						b.Fatal(err)
					}
					response := &topologyResponse{TraceID: "abc", Data: make([]stdjson.RawMessage, 1), chunks: [][][]byte{chunks}}
					parts, err := response.JSONSegments()
					if err != nil {
						b.Fatal(err)
					}
					size := 0
					for _, part := range parts {
						size += len(part)
					}
					b.ReportMetric(float64(size), "wire-bytes")
				}
			}
		})
	}
}

func largeTopologyBenchmarkItem() cmdb.SharedTopologyResponseData {
	nodes := make([]cmdb.SharedTopologyNode, 136)
	edges := make([]cmdb.SharedTopologyEdge, 136)
	for i := range nodes {
		nodes[i] = cmdb.SharedTopologyNode{ID: uint64(i + 1), ResourceType: "node", Dimensions: cmdb.Matcher{"name": "representative-node", "cluster": "BCS-K8S-00000"}}
		edges[i] = cmdb.SharedTopologyEdge{Source: uint64(i + 1), Target: uint64(i + 1), RelationType: "node_with_system"}
	}
	item := cmdb.SharedTopologyResponseData{Code: http.StatusOK, Step: "1s", PointCount: 512, Snapshots: make([]cmdb.SharedTopologySnapshot, 512)}
	for i := range item.Snapshots {
		item.Snapshots[i] = cmdb.SharedTopologySnapshot{Timestamp: int64(1700000000 + i), Nodes: nodes, Edges: edges}
	}
	return item
}

func TestTopologyEncodedItemsReusePreservesNilEmptyAndMixedResults(t *testing.T) {
	for _, items := range [][]stdjson.RawMessage{nil, {}, {nil, stdjson.RawMessage(`{"code":400,"message":"bad"}`), stdjson.RawMessage(`{"code":200,"snapshots":[]}`)}} {
		response := &topologyResponse{TraceID: "x", Data: items}
		expected, err := stdjson.Marshal(response)
		require.NoError(t, err)
		parts, err := response.JSONSegments()
		require.NoError(t, err)
		if items == nil {
			require.Nil(t, parts)
			continue
		}
		require.JSONEq(t, string(expected), string(bytes.Join(parts, nil)))
		for _, item := range items {
			if len(item) > 0 {
				found := false
				for _, part := range parts {
					if len(part) > 0 && &part[0] == &item[0] {
						found = true
					}
				}
				require.True(t, found, "encoded item copied instead of reused")
			}
		}
	}
}
