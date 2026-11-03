package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// largeAnswerSeries is the size of a per-core CPU strategy's answer over a
// fleet of hosts: one series for each core of 1,683 hosts.
const largeAnswerSeries = 71_668

// largeAnswerAttempt is a one-minute query whose series are named by host,
// cloud area and device, as a per-core CPU strategy groups them.
func largeAnswerAttempt(tb testing.TB) queryIdentity {
	tb.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu_detail", FieldName: "usage",
			ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			TimeAggregation: execution.QueryFunction{Method: "max", Position: 0, Window: "60s"},
			Dimensions:      []string{"bk_target_cloud_id", "bk_target_ip", "device_name"}, OffsetForward: "false"}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"bk_target_cloud_id", "bk_target_ip", "device_name"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		tb.Fatal(err)
	}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
		LogicalWindow: execution.QueryWindow{Start: 1_700_123_940, End: 1_700_124_000},
		ProviderRange: execution.QueryWindow{Start: 1_700_123_940, End: 1_700_124_000},
		AcceptedRange: execution.QueryWindow{Start: 1_700_123_940, End: 1_700_124_000}, RequiredColumns: []string{"value"}})
	if err != nil {
		tb.Fatal(err)
	}
	return queryIdentity{Spec: spec, AttemptNo: 1}
}

// largeAnswerBody is a shared-schema answer of series series, one point each,
// framed as the query service frames it: one schema, full frames of series.
func largeAnswerBody(tb testing.TB, series int) []byte {
	tb.Helper()
	var body strings.Builder
	seq := 0
	write := func(key string, value any) {
		raw, err := json.Marshal(map[string]any{"seq": seq, key: value})
		if err != nil {
			tb.Fatal(err)
		}
		body.Write(raw)
		body.WriteByte('\n')
		seq++
	}
	write("v", 1)
	write("schemas", []sharedSchema{{ID: 0, Columns: []string{"_time", "_value"}, Types: []string{"float", "float"},
		GroupKeys: []string{"bk_target_cloud_id", "bk_target_ip", "device_name"}}})
	frame := make([]any, 0, sharedFrameSeries)
	for index := 0; index < series; index++ {
		host, core := index/43, index%43
		frame = append(frame, map[string]any{"s": 0,
			"g": []json.RawMessage{json.RawMessage(`"0"`), json.RawMessage(fmt.Sprintf(`"10.%d.%d.%d"`, host/65536, host/256%256, host%256)),
				json.RawMessage(fmt.Sprintf(`"cpu%d"`, core))},
			"r": [][]json.RawMessage{{json.RawMessage(`1700123940000`), json.RawMessage(fmt.Sprintf(`%d.%d`, index%100, index%7))}}})
		if len(frame) == sharedFrameSeries || index == series-1 {
			write("series", frame)
			frame = frame[:0]
		}
	}
	write("end", map[string]any{"series": series, "points": series, "is_partial": false})
	return []byte(body.String())
}

// deliveryFoldingSink keeps only what the delivery chain folds, the way a consumer
// that hands every series on keeps nothing of it.
type deliveryFoldingSink struct {
	delivery execution.SeriesDelivery
}

func (sink *deliveryFoldingSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	var err error
	sink.delivery, err = execution.AccumulateSeriesDelivery(sink.delivery, batch.Delivery)
	return err
}

// The decode of a per-core CPU strategy's whole answer - framing, the
// canonical series and their digests - per answer and per series.
func BenchmarkDecodeALargeAnswer(b *testing.B) {
	attempt := largeAnswerAttempt(b)
	body := largeAnswerBody(b, largeAnswerSeries)
	limits := DefaultLimits()
	limits.MaxBodyBytes = 1 << 30
	client, err := NewClientWithLimits("http://127.0.0.1:1", "alarmd-shadow", &http.Client{}, limits)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink := &deliveryFoldingSink{}
		done, err := client.decodeShared(context.Background(), strings.NewReader(string(body)), attempt, sink)
		if err != nil || done.Completeness != execution.CompletenessFull || done.Delivery.Series != largeAnswerSeries {
			b.Fatalf("decode = %+v, %v", done, err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/largeAnswerSeries, "ns/series")
}
