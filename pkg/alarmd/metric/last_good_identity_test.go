// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A refresh round that refused last-good Plans for a changed identity adds
// that many to its own counter, apart from the stale-revision one: the two
// send the reader to different changes -- a binary, or a writer's numbering.
func TestTheLastGoodIdentityCounterCountsItsOwnRefusals(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	refresh := func(stale, identity int) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed, Result: observability.ResultSuccess,
			SourceRefresh: &observability.SourceRefreshFacts{Status: observability.AllSourceRefreshStatuses()[0],
				RetainedStaleRevisions: stale, LastGoodIdentityChanged: identity},
		})
	}
	refresh(0, 2)
	refresh(1, 0)
	if got := testutil.ToFloat64(recorder.phaseTwo.controlSourceLastGoodIdentity); got != 2 {
		t.Fatalf("identity changed = %v, want 2", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.controlSourceRetainedStale); got != 1 {
		t.Fatalf("stale revisions = %v, want 1: the two counters do not share refusals", got)
	}
}
