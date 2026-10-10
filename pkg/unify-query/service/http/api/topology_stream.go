package api

import (
	stdjson "encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const largeTopologyEncodingElementThreshold = 200000

func marshalTopologyItem(payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err == nil && !utf8.Valid(encoded) {
		return stdjson.Marshal(payload)
	}
	return encoded, err
}

type topologyResponse struct {
	TraceID string               `json:"trace_id"`
	Data    []stdjson.RawMessage `json:"data"`
	chunks  [][][]byte
}

// JSONSegments returns nil for ordinary responses so their rendering is unchanged.
func (r *topologyResponse) JSONSegments() ([][]byte, error) {
	if r.chunks == nil {
		return nil, nil
	}
	if len(r.chunks) != len(r.Data) {
		return nil, fmt.Errorf("topology response has %d chunk lists for %d items", len(r.chunks), len(r.Data))
	}
	traceID, err := stdjson.Marshal(r.TraceID)
	if err != nil {
		return nil, err
	}
	parts := [][]byte{[]byte(`{"trace_id":`), traceID, []byte(`,"data":[`)}
	for index, item := range r.Data {
		if index != 0 {
			parts = append(parts, []byte(","))
		}
		if len(r.chunks[index]) != 0 {
			parts = append(parts, r.chunks[index]...)
		} else if len(item) == 0 {
			parts = append(parts, []byte("null"))
		} else {
			parts = append(parts, item)
		}
	}
	return append(parts, []byte("]}")), nil
}

// marshalLargeTopologySnapshots encodes each frame before response writing,
// preserving the ability to report an encoding failure as an HTTP error.
func marshalLargeTopologySnapshots(item cmdb.SharedTopologyResponseData) ([][]byte, int, error) {
	header, err := stdjson.Marshal(struct {
		Code       int    `json:"code"`
		StartTime  int64  `json:"start_time"`
		EndTime    int64  `json:"end_time"`
		Step       string `json:"step"`
		PointCount int    `json:"point_count"`
	}{item.Code, item.StartTime, item.EndTime, item.Step, item.PointCount})
	if err != nil {
		return nil, 0, err
	}
	parts := make([][]byte, 0, len(item.Snapshots)+5)
	parts = append(parts, append(header[:len(header)-1], []byte(`,"snapshots":[`)...))
	for index := range item.Snapshots {
		encoded, err := stdjson.Marshal(item.Snapshots[index])
		if err != nil {
			return nil, 0, err
		}
		if index+1 < len(item.Snapshots) {
			encoded = append(encoded, ',')
		}
		parts = append(parts, encoded)
	}
	parts = append(parts, []byte("]"))
	if item.Message != "" {
		encoded, err := stdjson.Marshal(item.Message)
		if err != nil {
			return nil, 0, err
		}
		parts = append(parts, []byte(`,"message":`), encoded)
	}
	parts = append(parts, []byte("}"))
	size := 0
	for _, part := range parts {
		if len(part) > int(^uint(0)>>1)-size {
			return nil, 0, fmt.Errorf("topology response size overflows int")
		}
		size += len(part)
	}
	return parts, size, nil
}
