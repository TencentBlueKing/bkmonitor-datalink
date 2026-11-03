// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// The reasons this package hands observations at run time, from lists that
// live elsewhere, are words the catalogue keeps: a no-data stall's outcome is
// its reason, and a Slot the ownership store refused names the refusal. The
// stall list in the catalogue is exactly the skips, so an outcome added to
// nodata fails here rather than reading _other on its first stall.
func TestEveryReasonThisPackageComputesIsCatalogued(t *testing.T) {
	known := map[observability.ReasonCode]bool{}
	for _, reason := range observability.AllLogReasons() {
		known[reason] = true
	}
	stalls := map[observability.ReasonCode]bool{}
	for _, reason := range observability.NoDataStallReasons {
		stalls[reason] = true
	}
	skips := 0
	for _, outcome := range nodata.SlotOutcomes {
		if outcome == nodata.OutcomeEvaluated || outcome == nodata.OutcomeNone {
			continue
		}
		skips++
		reason := observability.ReasonCode(outcome)
		if !stalls[reason] || !known[reason] || observability.NormalizeReason(reason, observability.ResultDegraded) != reason {
			t.Errorf("no-data outcome %q is not a catalogued stall reason", outcome)
		}
	}
	if skips != len(stalls) {
		t.Errorf("%d skip outcomes, %d stall reasons: the two lists differ", skips, len(stalls))
	}
	for _, refusal := range []error{ownership.ErrNotDesired, ownership.ErrLeaseBusy, ownership.ErrStaleFence, ownership.ErrContentScopeMoved} {
		word, refused := ownership.RefusalReason(refusal)
		if !refused || !known[observability.ReasonCode(word)] {
			t.Errorf("ownership refusal %v names %q, which the catalogue does not keep", refusal, word)
		}
	}
}
