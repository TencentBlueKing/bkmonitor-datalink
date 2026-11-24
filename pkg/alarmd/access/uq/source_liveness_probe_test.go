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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// The answers design §8 read from the query service for each probe shape,
// and what the probe makes of them: one series of counts is alive; no
// series with a status (an unknown table, field or space) is unknown by
// status; no series and no status is unknown, empty without status, in this
// version; a partial answer is unknown; an answer that does not come back
// in the probe's time is unknown by timeout.
func TestAProbeReadsTheShapesTheQueryServiceAnswers(t *testing.T) {
	facts := livenessFacts(t, false, "a",
		`{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary","metric_field":"usage","agg_method":"AVG","agg_interval":60,"alias":"a"}`)
	probe, _, ok := sourceliveness.Derive(facts, judged-180, judged, 180)
	if !ok {
		t.Fatal("no probe")
	}
	for _, test := range []struct {
		name, body string
		hang       bool
		want       sourceliveness.Verdict
	}{
		{name: "data", want: sourceliveness.Verdict{Result: sourceliveness.ResultAlive},
			body: `{"series":[{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1700123940000,24],[1700124000000,23]]}],"status":null,"is_partial":false}`},
		{name: "no series, a status", want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonStatus},
			body: `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS","message":"x"},"is_partial":false}`},
		{name: "no series, no status", want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonEmptyWithoutStatus},
			body: `{"series":[],"status":null,"is_partial":false}`},
		{name: "zero counts", want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonEmptyWithoutStatus},
			body: `{"series":[{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1700123940000,0]]}],"status":null,"is_partial":false}`},
		{name: "partial", want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonPartial},
			body: `{"series":[{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1700123940000,3]]}],"status":null,"is_partial":true}`},
		{name: "without is_partial", want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonError},
			body: `{"series":[],"status":null}`},
		{name: "no answer in time", hang: true, want: sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonTimeout}},
	} {
		var gotPath, gotSpace string
		var gotBody []byte
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			if gotBody == nil {
				// The probe ahead; the round's own follows it.
				gotPath, gotSpace, gotBody = request.URL.Path, request.Header.Get(headerSpace), body
			}
			if test.hang {
				<-request.Context().Done()
				return
			}
			_, _ = writer.Write([]byte(test.body))
		}))
		client, err := NewClient(server.URL, "alarmd", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		checker, err := sourceliveness.NewChecker(context.Background(), client, nil)
		if err != nil {
			t.Fatal(err)
		}
		checker.WithProbeTimeout(200 * time.Millisecond)
		checker.Ahead(facts, judged-180, judged, 180)
		checker.Wait()
		got := checker.Judge(sourceliveness.Subject{LastRealPoint: judged - 180}, facts, judged+60)
		checker.Wait()
		server.Close()
		if got != test.want {
			t.Errorf("%s: verdict %+v, want %+v", test.name, got, test.want)
		}
		_, wantBody, _, _ := buildProbeWire(probe)
		if gotPath != "/query/ts" || gotSpace != "bkcc__2" || string(gotBody) != string(wantBody) {
			t.Errorf("%s: asked %s in %q with %s, want the probe", test.name, gotPath, gotSpace, gotBody)
		}
	}
}
