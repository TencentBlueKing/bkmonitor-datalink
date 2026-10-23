package uq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// framedSeries are series series of a per-core CPU answer, one point each but
// for bad, whose two points come out of order: no series can be made of it.
func framedSeries(series, bad int) []responseSeries {
	all := make([]responseSeries, 0, series)
	for index := 0; index < series; index++ {
		item := responseSeries{Columns: []string{"_time", "_value"}, Types: []string{"float", "float"},
			GroupKeys: []string{"bk_target_cloud_id", "bk_target_ip", "device_name"},
			GroupValues: []json.RawMessage{json.RawMessage(`"0"`), json.RawMessage(fmt.Sprintf(`"10.0.%d.%d"`, index/43/256, index/43%256)),
				json.RawMessage(fmt.Sprintf(`"cpu%d"`, index%43))},
			Values: [][]json.RawMessage{{json.RawMessage(`1700123940000`), json.RawMessage(fmt.Sprintf(`%d.%d`, index%100, index%7))}}}
		if index == bad {
			item.Values = [][]json.RawMessage{{json.RawMessage(`1700123960000`), json.RawMessage(`1`)}, {json.RawMessage(`1700123940000`), json.RawMessage(`2`)}}
		}
		all = append(all, item)
	}
	return all
}

// framedBody frames series as the query service does: one schema, full
// frames of series, and the footer counting them.
func framedBody(t *testing.T, series []responseSeries) string {
	t.Helper()
	var body strings.Builder
	seq, points := 0, 0
	write := func(key string, value any) {
		raw, err := json.Marshal(map[string]any{"seq": seq, key: value})
		if err != nil {
			t.Fatal(err)
		}
		body.Write(raw)
		body.WriteByte('\n')
		seq++
	}
	write("v", 1)
	write("schemas", []sharedSchema{{ID: 0, Columns: series[0].Columns, Types: series[0].Types, GroupKeys: series[0].GroupKeys}})
	frame := make([]any, 0, sharedFrameSeries)
	for index, item := range series {
		frame = append(frame, map[string]any{"s": 0, "g": item.GroupValues, "r": item.Values})
		points += len(item.Values)
		if len(frame) == sharedFrameSeries || index == len(series)-1 {
			write("series", frame)
			frame = frame[:0]
		}
	}
	write("end", map[string]any{"series": len(series), "points": points, "is_partial": false})
	return body.String()
}

// failingSink keeps what it is handed, and refuses the series at failAt.
type failingSink struct {
	batches []execution.ProviderSeriesBatch
	failAt  int
}

var errSinkRefused = errors.New("sink refused the series")

func (sink *failingSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	if len(sink.batches) == sink.failAt {
		return errSinkRefused
	}
	sink.batches = append(sink.batches, batch)
	return nil
}

// oneAtATime is what an answer of series came to when each was made
// canonical and handed on as it came, before the next was read: the batches
// handed on, the delivery they chain to, and the error that ended it.
func oneAtATime(t *testing.T, client *Client, attempt queryIdentity, series []responseSeries, failAt int) ([]execution.ProviderSeriesBatch, execution.SeriesDelivery, error) {
	t.Helper()
	sink := &failingSink{failAt: failAt}
	var delivery execution.SeriesDelivery
	for _, item := range series {
		size, err := expandedSeriesBytes(item, uint64(client.limits.MaxSeriesBytes))
		if err != nil {
			t.Fatal(err)
		}
		batch, _, err := normalizeSeries(attempt.Spec, providerResultRef(attempt), item, client.now().Unix())
		if err != nil {
			return sink.batches, delivery, err
		}
		batch.Delivery.Bytes = size
		if err := sink.ConsumeProviderSeries(context.Background(), batch); err != nil {
			return sink.batches, delivery, err
		}
		if delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery); err != nil {
			t.Fatal(err)
		}
	}
	return sink.batches, delivery, nil
}

// Series made canonical side by side are handed on as they were one at a
// time: the same batches in the same order, the same delivery digest chain,
// and, where a series cannot be made canonical or the sink refuses one, the
// same series handed on before it and the same error. Run with -cpu 1,4: one
// CPU makes every series itself, four hand them to workers. With every worker
// taken - every CPU busy with answers of its own - an answer makes its series
// itself, and the same; and no answer keeps a worker once it is read.
func TestSeriesMadeCanonicalSideBySideAreHandedOnAsOneAtATime(t *testing.T) {
	attempt := largeAnswerAttempt(t)
	client, err := NewClientWithLimits("http://127.0.0.1:1", "alarmd-shadow", &http.Client{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// The answer's received time is in every record and so in every digest.
	client.now = func() time.Time { return time.Unix(1_700_124_005, 0) }
	t.Logf("GOMAXPROCS %d, %d workers", runtime.GOMAXPROCS(0), normalizeWorkers())
	for _, test := range []struct {
		name        string
		series, bad int
		failAt      int
	}{
		{name: "whole", series: 3_000, bad: -1, failAt: -1},
		{name: "a series that cannot be made canonical", series: 3_000, bad: 1_234, failAt: -1},
		{name: "a series the sink refuses", series: 3_000, bad: -1, failAt: 1_500},
	} {
		t.Run(test.name, func(t *testing.T) {
			series := framedSeries(test.series, test.bad)
			wantBatches, wantDelivery, wantErr := oneAtATime(t, client, attempt, series, test.failAt)
			for run := 0; run < 6; run++ {
				taken := 0
				if run == 5 {
					for tryNormalizeWorker() {
						taken++
					}
				}
				sink := &failingSink{failAt: test.failAt}
				done, err := client.decodeShared(context.Background(), strings.NewReader(framedBody(t, series)), attempt, sink)
				if fmt.Sprint(err) != fmt.Sprint(wantErr) {
					t.Fatalf("run %d: error %v, want %v", run, err, wantErr)
				}
				if len(sink.batches) != len(wantBatches) {
					t.Fatalf("run %d: handed on %d series, want %d", run, len(sink.batches), len(wantBatches))
				}
				for index := range wantBatches {
					got, want := sink.batches[index], wantBatches[index]
					if got.Delivery != want.Delivery || !reflect.DeepEqual(got.Dataset.Records(), want.Dataset.Records()) {
						t.Fatalf("run %d: series %d handed on as %+v, want %+v", run, index, got.Delivery, want.Delivery)
					}
				}
				if wantErr == nil && done.Delivery != wantDelivery {
					t.Fatalf("run %d: delivery %+v, want %+v", run, done.Delivery, wantDelivery)
				}
				if busy := normalizeWorkersBusy.Load(); busy != int64(taken) {
					t.Fatalf("run %d: %d workers taken after the answer was read, want the %d held", run, busy, taken)
				}
				for ; taken > 0; taken-- {
					releaseNormalizeWorker()
				}
			}
		})
	}
}
