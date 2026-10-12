// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// noDataTriggerBeyondHorizon is the warning for a Plan whose no-data trigger
// can never fire before the tracking horizon stops the absence, nil for one
// that can.
//
// The arithmetic is the contract's, not this build's: tracking stops on the
// round where EvaluationTime - first_absent >= H, and that round gives no
// verdict (retention proposal, section 1 item 2); the trigger needs
// continuous consecutive absent points one period apart (P9), so the earliest
// it can fire is first_absent + (continuous - 1) * period. It fires only when
// that is less than H.
//
// A warning and nothing more, because the owner can run this and may mean to
// (the user's rule of 2026-10-08: warn, do not refuse what can be executed):
// the Plan stays accepted, its no-data is tracked, stopped and recovered as
// configured, and no partition count moves. It is listed under
// CONFIG_NOTED, the disposition a running Plan's configuration notes
// live under, with the numbers the owner chooses between.
//
// The best case only. A round that did not see the whole period does not
// count as absent (no bridging), so a gap inside the window pushes the
// earliest alert later while the horizon still runs from the original start;
// a configuration just under the line can still stop first at run time,
// which is the expiry the contract already describes, not a separate rule.
func noDataTriggerBeyondHorizon(sourceID string, config *contract.NoDataConfigV1, periodSeconds int64) *ObjectDisposition {
	if config == nil || config.TrackingHorizonSeconds <= 0 || config.Continuous == 0 || periodSeconds <= 0 {
		return nil
	}
	earliest := int64(config.Continuous-1) * periodSeconds
	if earliest < config.TrackingHorizonSeconds {
		return nil
	}
	return &ObjectDisposition{
		SourceID: sourceID, Scope: "PLAN", Disposition: DispositionConfigNoted,
		Reason: ReasonNoDataTriggerBeyondHorizon, FieldPath: "items[0].no_data_config.continuous",
		Detail: fmt.Sprintf("continuous=%d period=%d earliest_alert_after=%d tracking_horizon=%d horizon_source=%s",
			config.Continuous, periodSeconds, earliest, config.TrackingHorizonSeconds, config.TrackingHorizonSource),
	}
}
