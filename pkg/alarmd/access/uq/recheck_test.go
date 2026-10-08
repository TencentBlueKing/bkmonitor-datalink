// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A recheck is the formal read again: the same request on the wire, and the
// same series identities and record ids out of the same answer.
func TestARecheckIsTheFormalReadAgain(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var headers []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies, headers = append(bodies, raw), append(headers, r.Header.Clone())
		mu.Unlock()
		_, _ = io.WriteString(w, `{"series":[`+diagnosticSeries("192.0.2.1", 3)+`,`+diagnosticSeries("192.0.2.2", 2)+`],"is_partial":false}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	formal := &collectingSink{}
	if _, err := client.Execute(context.Background(), attempt, formal); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	again := &collectingSink{}
	completion, err := client.Recheck(ctx, attempt.Spec, again)
	if err != nil || completion.Completeness != execution.CompletenessFull {
		t.Fatalf("recheck = %+v %v", completion, err)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("the recheck sent another request:\n%s\n%s", bodies[0], bodies[1])
	}
	for _, name := range []string{headerTenant, headerSpace, headerQuerySource} {
		if headers[0].Get(name) != headers[1].Get(name) {
			t.Fatalf("header %s differs: %q vs %q", name, headers[0].Get(name), headers[1].Get(name))
		}
	}
	if len(formal.batches) != 2 || len(again.batches) != 2 {
		t.Fatalf("series formal %d recheck %d, want 2 and 2", len(formal.batches), len(again.batches))
	}
	for i := range formal.batches {
		first, second := formal.batches[i].Dataset, again.batches[i].Dataset
		if first.Len() != second.Len() {
			t.Fatalf("series %d has %d records then %d", i, first.Len(), second.Len())
		}
		for j := 0; j < first.Len(); j++ {
			a, _ := first.Record(j)
			b, _ := second.Record(j)
			if a.RecordID() != b.RecordID() || a.DimensionIdentityDigest() != b.DimensionIdentityDigest() {
				t.Fatalf("record %d/%d identity changed: %s/%s vs %s/%s", i, j, a.RecordID(), a.DimensionIdentityDigest(), b.RecordID(), b.DimensionIdentityDigest())
			}
		}
	}
}

func TestARecheckRefusesWhatItCannotBound(t *testing.T) {
	client, err := NewClient("http://127.0.0.1:1", "alarmd", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	spec := validAttempt(t).Spec
	if _, err := client.Recheck(context.Background(), spec, &collectingSink{}); err == nil {
		t.Fatal("a recheck without a deadline ran")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Recheck(ctx, execution.PhysicalQuerySpec{}, &collectingSink{}); err == nil {
		t.Fatal("a recheck of no query ran")
	}
	if _, err := client.Recheck(ctx, spec, nil); err == nil {
		t.Fatal("a recheck with no sink ran")
	}
}
