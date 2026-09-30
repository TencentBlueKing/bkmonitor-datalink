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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The store names its kinds in execution and the counter names its labels in
// observability, as two literal lists. A kind the store starts reporting that
// observability does not name folds into other silently, and other rising is
// the only trace -- a label with a writer nobody wired. Every kind the store
// can report has to survive normalisation, and the published pair count has
// to be derived from the same two lists.
func TestEveryStateKindTheStoreReportsSurvivesObservabilityNormalisation(t *testing.T) {
	for _, kind := range execution.AllStateVersionConflictKinds() {
		mirrored := observability.StateVersionConflictKind(kind)
		if got := observability.NormalizeStateVersionConflictKind(mirrored); got != mirrored {
			t.Fatalf("version conflict kind %q normalises to %q: observability does not name it", kind, got)
		}
	}
	for _, kind := range execution.AllStateAlreadyAppliedKinds() {
		mirrored := observability.StateAlreadyAppliedKind(kind)
		if got := observability.NormalizeStateAlreadyAppliedKind(mirrored); got != mirrored {
			t.Fatalf("already-applied kind %q normalises to %q: observability does not name it", kind, got)
		}
	}
	// observability publishes every store kind plus other, and nothing else:
	// a label published for a kind no store reports is a series that can
	// only ever read zero.
	if want, got := len(execution.AllStateVersionConflictKinds())+1, len(observability.AllStateVersionConflictKinds()); got != want {
		t.Fatalf("published version conflict kinds = %d, want the store's %d plus other", got, want-1)
	}
	if want, got := len(execution.AllStateAlreadyAppliedKinds())+1, len(observability.AllStateAlreadyAppliedKinds()); got != want {
		t.Fatalf("published already-applied kinds = %d, want the store's %d plus other", got, want-1)
	}
	gathered := gatherStateVersionConflict(t)
	if want := len(observability.AllStateAlreadyAppliedSites()) * len(observability.AllStateVersionConflictKinds()); len(gathered) != want {
		t.Fatalf("published version conflict pairs = %d, want sites x kinds = %d", len(gathered), want)
	}
}
