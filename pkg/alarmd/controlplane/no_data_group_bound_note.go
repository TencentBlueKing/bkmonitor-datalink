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

// noDataGroupBoundAboveDefault is the note for a Plan whose own no-data group
// bound is above the default, nil for one that states none or one at or below
// it.
//
// A note and nothing more: the bound is the owner's to choose, up to the
// ceiling, and the Plan runs with it. What the owner is choosing is a one-off
// Redis cost on the Plan's first whole-record write per state generation, so
// the detail carries the bound, the default and the measured cost. The
// deployment's own bound above the default is noted once at startup instead,
// since it is not any one Plan's.
func noDataGroupBoundAboveDefault(sourceID string, config *contract.NoDataConfigV1) *ObjectDisposition {
	if config == nil || config.MaxGroups <= contract.DefaultMaxNoDataGroups {
		return nil
	}
	return &ObjectDisposition{
		SourceID: sourceID, Scope: "PLAN", Disposition: DispositionConfigNoted,
		Reason: ReasonNoDataGroupBoundAboveDefault, FieldPath: "items[0].no_data_config.max_groups",
		Detail: fmt.Sprintf("max_groups=%d default=%d; %s",
			config.MaxGroups, contract.DefaultMaxNoDataGroups, contract.NoDataGroupFirstWriteCost),
	}
}
