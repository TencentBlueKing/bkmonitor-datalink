// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// hostPlacementObserver counts every admitted series a Plan read whose host
// CMDB placed by id or agent, by how its address compares, and samples the
// ones whose address differs. A series whose address is the host's takes
// the counter alone: no lock, no allocation.
func hostPlacementObserver(recorder *metric.Recorder, samples *fleet.AddressDiffersSamples, now func() time.Time) func(access.HostPlacement) {
	return func(placement access.HostPlacement) {
		recorder.RecordHostPlacement(placement.GroupedByTargetIP, placement.Address.Cause)
		if !placement.Address.Differs() {
			return
		}
		address := placement.Address
		reported := address.Reported
		if reported == "" {
			reported = address.Cause
		}
		samples.Record(fleet.AddressDiffersSample{At: now().UTC(), Cause: address.Cause,
			StrategyID: placement.Plan.StrategyID, Tenant: placement.Plan.TenantID, Business: placement.Plan.BusinessID,
			QueryGroup: string(placement.QueryGroup), HostID: address.HostID, AgentID: address.AgentID,
			Reported: reported, CMDBAddress: address.CMDB})
	}
}
