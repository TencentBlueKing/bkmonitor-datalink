package api

import (
	stdjson "encoding/json"
	"fmt"
)

type topologyResponse struct {
	TraceID string               `json:"trace_id"`
	Data    []stdjson.RawMessage `json:"data"`
	chunks  [][][]byte
}

// JSONSegments reuses owned item buffers without encoding the outer JSON again.
func (r *topologyResponse) JSONSegments() ([][]byte, error) {
	if r.chunks == nil && r.Data == nil {
		return nil, nil
	}
	if r.chunks != nil && len(r.chunks) != len(r.Data) {
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
		if r.chunks != nil && len(r.chunks[index]) != 0 {
			parts = append(parts, r.chunks[index]...)
		} else if len(item) == 0 {
			parts = append(parts, []byte("null"))
		} else {
			parts = append(parts, item)
		}
	}
	return append(parts, []byte("]}")), nil
}
