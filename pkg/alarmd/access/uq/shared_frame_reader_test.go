package uq

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// A shared-schema response reads through a pooled frame buffer: the 256 KiB a
// whole frame may take is not allocated again for every response. A small
// response here allocates a few KB to decode, against the 256 KiB a reader of
// its own would cost.
func TestASharedResponseDoesNotAllocateItsFrameBuffer(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("the race detector's sync.Pool drops a quarter of what is put back")
	}
	client := fixtureClient(t, http.StatusOK, "", DefaultLimits())
	identity := queryIdentity{Spec: validAttempt(t).Spec, AttemptNo: 1}
	body := sharedTestBody(t, []responseSeries{sharedTestSeries()}, map[string]any{"is_partial": false})
	decode := func() {
		done, err := client.decodeShared(context.Background(), strings.NewReader(body), identity, &collectingSink{})
		if err != nil || done.Completeness != execution.CompletenessFull {
			t.Fatalf("decode = %+v, %v", done, err)
		}
	}
	decode()
	const runs = 50
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < runs; i++ {
		decode()
	}
	runtime.ReadMemStats(&after)
	if perResponse := (after.TotalAlloc - before.TotalAlloc) / runs; perResponse >= sharedFrameBytes/4 {
		t.Fatalf("%d B allocated per response, want well under the %d B frame buffer", perResponse, sharedFrameBytes)
	}
}

// A pooled buffer carries nothing from one response into another: what a
// response delivered stays as it was after the buffer reads the next, a
// response that overran a frame leaves the buffer full of its bytes and the
// next response still decodes whole.
func TestAPooledFrameBufferCarriesNothingIntoTheNextResponse(t *testing.T) {
	client := fixtureClient(t, http.StatusOK, "", DefaultLimits())
	identity := queryIdentity{Spec: validAttempt(t).Spec, AttemptNo: 1}
	first := sharedTestSeries()
	second := sharedTestSeries()
	second.GroupValues = []json.RawMessage{json.RawMessage(`"192.0.2.7"`)}
	second.Values = [][]json.RawMessage{{json.RawMessage(`1700123000000`), json.RawMessage(`99.5`)}, {json.RawMessage(`1700124000000`), json.RawMessage(`98.5`)}}
	snapshot := func(sink *collectingSink) string {
		var records []any
		for _, batch := range sink.batches {
			for _, record := range batch.Dataset.Records() {
				records = append(records, record)
			}
		}
		raw, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	firstSink := &collectingSink{}
	if done, err := client.decodeShared(context.Background(), strings.NewReader(sharedTestBody(t, []responseSeries{first}, map[string]any{"is_partial": false})), identity, firstSink); err != nil || done.Completeness != execution.CompletenessFull {
		t.Fatalf("first response = %+v, %v", done, err)
	}
	delivered := snapshot(firstSink)
	if !strings.Contains(delivered, "127.0.0.1") {
		t.Fatalf("first response delivered %s, want its series", delivered)
	}
	overrun := `{"seq":0,"v":1,"padding":"` + strings.Repeat("x", sharedFrameBytes) + `"}` + "\n"
	if done, err := client.decodeShared(context.Background(), strings.NewReader(overrun), identity, &collectingSink{}); err != nil || done.RouteFacts.Attempts[0].Detail != "response=limit_frame_bytes" {
		t.Fatalf("overrun response = %+v, %v", done, err)
	}
	secondSink := &collectingSink{}
	if done, err := client.decodeShared(context.Background(), strings.NewReader(sharedTestBody(t, []responseSeries{second}, map[string]any{"is_partial": false})), identity, secondSink); err != nil || done.Completeness != execution.CompletenessFull {
		t.Fatalf("response after an overrun = %+v, %v", done, err)
	}
	if got := snapshot(firstSink); got != delivered {
		t.Fatalf("the first response's delivery changed after the buffer read others:\n%s\nwant\n%s", got, delivered)
	}
	if got := snapshot(secondSink); !strings.Contains(got, "192.0.2.7") || strings.Contains(got, "127.0.0.1") || strings.Contains(got, "xxxx") {
		t.Fatalf("the response after an overrun delivered %s, want its own series only", got)
	}
}
