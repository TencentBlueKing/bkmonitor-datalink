package api

import (
	"bytes"
	stdjson "encoding/json"
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

func TestTopologyItemEncodingRepairsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	item := cmdb.SharedTopologyResponseData{Code: http.StatusOK, Snapshots: []cmdb.SharedTopologySnapshot{{Nodes: []cmdb.SharedTopologyNode{{Dimensions: cmdb.Matcher{"invalid": invalid}}}}}}
	compact := cmdb.CompactTopologyResponseData{Code: http.StatusOK, Compact: &cmdb.CompactTopology{Nodes: []cmdb.CompactTopologyNode{{Dimensions: cmdb.Matcher{"invalid": invalid}}}}}
	for _, payload := range []any{item, compact} {
		want, err := stdjson.Marshal(payload)
		require.NoError(t, err)
		encoded, err := marshalTopologyItem(payload)
		require.NoError(t, err)
		require.Equal(t, want, encoded)
		require.True(t, utf8.Valid(encoded))
	}
	chunks, _, err := marshalLargeTopologySnapshots(item)
	require.NoError(t, err)
	require.True(t, utf8.Valid(bytes.Join(chunks, nil)))
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
