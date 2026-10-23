// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Each physical query's completion names how long a range it asked the
// provider for and how long a range it accepts, and whether it serves a
// primary requirement: an event count's primary asks from its lead earlier
// and accepts its window; a query without a lead asks what it accepts.
func TestEachQueryCompletionNamesTheRangeItAsked(t *testing.T) {
	for name, test := range map[string]struct {
		lead int64
		want execution.PhysicalQueryRange
	}{
		"an event count's lead": {lead: 240, want: execution.PhysicalQueryRange{Primary: true, AskedSeconds: 300, AcceptedSeconds: 60}},
		"no lead":               {want: execution.PhysicalQueryRange{Primary: true, AskedSeconds: 60, AcceptedSeconds: 60}},
	} {
		t.Run(name, func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			frozen.Requirements[0].ProviderLeadSeconds = test.lead
			contractRef = bindFrozenDueDigest(t, contractRef, frozen)
			source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
			source.wait = func(context.Context, time.Duration) error { return nil }
			completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
				Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
			}, &recordingConsumer{})
			if err != nil {
				t.Fatal(err)
			}
			if len(completion.PhysicalQueries) != 1 || completion.PhysicalQueries[0].Range == nil ||
				*completion.PhysicalQueries[0].Range != test.want {
				t.Fatalf("physical queries = %+v, want the range %+v", completion.PhysicalQueries, test.want)
			}
		})
	}
}
