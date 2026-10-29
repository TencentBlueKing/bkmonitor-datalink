// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var errSlotFull = errors.New("the Slot has no room")

// slotPool is a Slot's retained budget as the access source's sink serves
// it: a series handed on is charged against the pool by the sink itself, at
// its delivered bytes and what the sink keeps beside them (seriesKept), and
// what a query reserves beside its series is reserved against the same
// pool. Either is refused past the limit, and the first refusal is said on
// refused. A sink may also refuse the series it is handed (refuseSeries,
// counted from 1) for a cause of its own.
type slotPool struct {
	limit, used       uint64
	reserved, charged uint64
	peakReserved      uint64
	// reservedAtFirstSeries is what was still reserved when the first
	// series was handed on.
	reservedAtFirstSeries uint64
	handed                []execution.ProviderSeriesBatch
	refuseSeries          int
	refused               chan struct{}
	refusedOnce           sync.Once
}

const seriesKept = 100

func newSlotPool(used, limit uint64) *slotPool {
	return &slotPool{used: used, limit: limit, refused: make(chan struct{})}
}

func (pool *slotPool) take(bytes uint64) error {
	if pool.used+bytes > pool.limit {
		pool.refusedOnce.Do(func() { close(pool.refused) })
		return errSlotFull
	}
	pool.used += bytes
	return nil
}

func (pool *slotPool) ReserveRetainedBytes(_ context.Context, bytes uint64) error {
	if err := pool.take(bytes); err != nil {
		return err
	}
	pool.reserved += bytes
	pool.peakReserved = max(pool.peakReserved, pool.reserved)
	return nil
}

func (pool *slotPool) ReleaseRetainedBytes(bytes uint64) {
	bytes = min(bytes, pool.reserved)
	pool.reserved -= bytes
	pool.used -= bytes
}

func (pool *slotPool) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	if len(pool.handed) == 0 {
		pool.reservedAtFirstSeries = pool.reserved
	}
	if len(pool.handed)+1 == pool.refuseSeries {
		return errors.New("the sink refused the series")
	}
	charge := batch.Delivery.Bytes + seriesKept
	if err := pool.take(charge); err != nil {
		return err
	}
	pool.charged += charge
	pool.handed = append(pool.handed, batch)
	return nil
}

func (pool *slotPool) handedBytes() uint64 {
	var total uint64
	for _, batch := range pool.handed {
		total += batch.Delivery.Bytes
	}
	return total
}

// namedAPMSeries is one series of service, carrying app when it is set.
func namedAPMSeries(service, app string) string {
	keys, values := `"service_name"`, `"`+service+`"`
	if app != "" {
		keys, values = keys+`,"app_name"`, values+`,"`+app+`"`
	}
	return `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[` + keys + `],"group_values":[` + values + `],"values":[[1700123460000,1],[1700123520000,2]]}`
}

func plainSpec(t *testing.T) execution.PhysicalQuerySpec {
	return compiledSpec(t, strings.Replace(apmConfig, "2_bkapm_metric_cross_bank_trade.__default__", "2_system.cpu_summary", 1))
}

// A carried read whose Slot has no room is refused at its first series, as
// a plain read is, not once its whole response is in: the server holds the
// rest of its 100 series back until the Slot has said no, so a read that
// waited for them would never be refused, and the query would end at its
// deadline instead. Nothing stays reserved after it.
func TestACarriedReadIsRefusedAtTheFirstSeriesItsSlotHasNoRoomFor(t *testing.T) {
	for _, carried := range []bool{true, false} {
		t.Run(fmt.Sprintf("carried=%v", carried), func(t *testing.T) {
			pool := newSlotPool(1000, 1000)
			var written atomic.Int64
			writtenAtRefusal := int64(-1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				flusher := writer.(http.Flusher)
				_, _ = writer.Write([]byte(`{"series":[`))
				for index := 0; index < 100; index++ {
					if index > 0 {
						_, _ = writer.Write([]byte(","))
					}
					app := ""
					if carried {
						app = "cross-bank-trade"
					}
					_, _ = writer.Write([]byte(namedAPMSeries(fmt.Sprintf("svc-%d", index), app)))
					flusher.Flush()
					written.Add(1)
					if index == 0 {
						select {
						case <-pool.refused:
							writtenAtRefusal = written.Load()
						case <-request.Context().Done():
						}
					}
				}
				_, _ = writer.Write([]byte(`],"status":null,"trace_id":"t","is_partial":false,"result_table_id":["2_bkapm_metric_cross_bank_trade"]}`))
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, "alarmd", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			attempt := apmAttempt(t)
			if !carried {
				attempt.Spec = plainSpec(t)
			}
			_, err = client.Execute(context.Background(), attempt, pool)
			server.Close()
			if !errors.Is(err, errSlotFull) {
				t.Fatalf("the read ended with %v, want the Slot's refusal", err)
			}
			if writtenAtRefusal != 1 {
				t.Fatalf("the Slot refused with %d of 100 series written, want at the first", writtenAtRefusal)
			}
			if pool.used != 1000 || pool.reserved != 0 || len(pool.handed) != 0 {
				t.Fatalf("after the refusal the pool holds %d (%d reserved, %d series), want the 1000 it began with", pool.used, pool.reserved, len(pool.handed))
			}
		})
	}
}

// A carried read holds its series on the Slot's budget while it waits for
// its response, and hands that hold on with them: what the Slot holds after
// delivery is what the sink charged for the series it took, as after a
// plain read, with nothing of the hold left. Each series' hold is given back
// before the sink charges it, so a Slot with exactly the room a plain read
// of the same series needs is not refused.
func TestACarriedReadHoldsItsSeriesOnTheSlotsBudgetAndHandsTheHoldOn(t *testing.T) {
	carried := namedAPMSeries("a", "cross-bank-trade") + "," + namedAPMSeries("b", "cross-bank-trade") + "," + namedAPMSeries("c", "cross-bank-trade")
	plain := namedAPMSeries("a", "") + "," + namedAPMSeries("b", "") + "," + namedAPMSeries("c", "")
	read := func(t *testing.T, spec execution.PhysicalQuerySpec, limit uint64) (*slotPool, []bool, error) {
		server, asked := apmServer(t, carried, plain)
		client, err := NewClient(server.URL, "alarmd", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		attempt := apmAttempt(t)
		attempt.Spec = spec
		pool := newSlotPool(1000, limit)
		_, err = client.Execute(context.Background(), attempt, pool)
		return pool, *asked, err
	}
	for _, tc := range []struct {
		name    string
		spec    func(*testing.T) execution.PhysicalQuerySpec
		carried bool
	}{
		{name: "carried", spec: func(t *testing.T) execution.PhysicalQuerySpec { return apmAttempt(t).Spec }, carried: true},
		{name: "plain", spec: plainSpec},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roomy, asked, err := read(t, tc.spec(t), 1<<30)
			if err != nil {
				t.Fatal(err)
			}
			if len(asked) != 1 || asked[0] != tc.carried || len(roomy.handed) != 3 {
				t.Fatalf("asked %v, handed %d series, want one read handing on all three", asked, len(roomy.handed))
			}
			if roomy.used != 1000+roomy.charged || roomy.reserved != 0 {
				t.Fatalf("after delivery the pool holds %d with %d reserved, want the 1000 it began with and the %d charged for the series", roomy.used, roomy.reserved, roomy.charged)
			}
			wantPeak, wantAtFirst := uint64(0), uint64(0)
			if tc.carried {
				wantPeak = roomy.handedBytes()
				wantAtFirst = wantPeak - roomy.handed[0].Delivery.Bytes
			}
			if roomy.peakReserved != wantPeak || roomy.reservedAtFirstSeries != wantAtFirst {
				t.Fatalf("held %d at most and %d when the first series was handed on, want %d and %d", roomy.peakReserved, roomy.reservedAtFirstSeries, wantPeak, wantAtFirst)
			}
			exact, _, err := read(t, tc.spec(t), 1000+roomy.charged)
			if err != nil || exact.used != exact.limit {
				t.Fatalf("with room for the series' charges only the read ended %v holding %d of %d", err, exact.used, exact.limit)
			}
		})
	}
}

// Whatever a carried read still holds is given back when it ends without
// handing it on: before its split response is read again, when its body
// fails partway, and when the sink refuses a series partway through the
// hand-off. The Slot then holds what it began with and the charges of the
// series the sink did take, and nothing reserved.
func TestACarriedReadGivesBackWhatItHoldsWhenItEndsWithoutHandingItOn(t *testing.T) {
	unique := namedAPMSeries("a", "cross-bank-trade") + "," + namedAPMSeries("b", "cross-bank-trade") + "," + namedAPMSeries("c", "cross-bank-trade")
	split := namedAPMSeries("a", "cross-bank-trade") + "," + namedAPMSeries("a", "cross-bank-trade-old") + "," + namedAPMSeries("b", "cross-bank-trade")
	for _, tc := range []struct {
		name         string
		answer       string
		truncated    bool
		refuseSeries int
		wantErr      bool
		wantAsked    []bool
		wantHanded   int
	}{
		{name: "read again", answer: split, wantAsked: []bool{true, false}, wantHanded: 1},
		{name: "body failed", answer: unique, truncated: true, wantErr: true, wantAsked: []bool{true}},
		{name: "sink refused", answer: unique, refuseSeries: 2, wantErr: true, wantAsked: []bool{true}, wantHanded: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []bool
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				withApp := strings.Contains(string(body), `"app_name"`)
				asked = append(asked, withApp)
				writer.Header().Set("Content-Type", "application/json")
				answer := namedAPMSeries("a", "")
				if withApp {
					answer = tc.answer
				}
				if tc.truncated {
					_, _ = writer.Write([]byte(`{"series":[` + answer + `,{"name":"_res`))
					return
				}
				_, _ = writer.Write([]byte(`{"series":[` + answer + `],"status":null,"trace_id":"t","is_partial":false,"result_table_id":["2_bkapm_metric_cross_bank_trade"]}`))
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, "alarmd", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			pool := newSlotPool(1000, 1<<30)
			pool.refuseSeries = tc.refuseSeries
			_, err = client.Execute(context.Background(), apmAttempt(t), pool)
			if (err != nil) != tc.wantErr {
				t.Fatalf("the read ended with %v", err)
			}
			if fmt.Sprint(asked) != fmt.Sprint(tc.wantAsked) || len(pool.handed) != tc.wantHanded {
				t.Fatalf("asked %v and handed on %d series, want %v and %d", asked, len(pool.handed), tc.wantAsked, tc.wantHanded)
			}
			if pool.peakReserved == 0 {
				t.Fatal("the carried response was never held on the Slot's budget")
			}
			if pool.used != 1000+pool.charged || pool.reserved != 0 {
				t.Fatalf("the pool holds %d with %d reserved, want the 1000 it began with and the %d charged for the series taken", pool.used, pool.reserved, pool.charged)
			}
			if tc.name == "read again" && pool.reservedAtFirstSeries != 0 {
				t.Fatalf("the read again began with %d of the split response still reserved", pool.reservedAtFirstSeries)
			}
		})
	}
}
