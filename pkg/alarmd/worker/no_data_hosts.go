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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// resolveNoDataHosts turns one Plan's host candidates into the two sets the
// evaluation reads, in a single pass over the index.
//
// One pass, because both answers come from the same lookup: the business a
// host is held under decides whether it is expected and whether it has left,
// and asking twice would be two reads per host for one fact. On a static target
// that is one map read per declared host per Slot - tens, not thousands - and
// the index is in-process, so the cost is the lookup and not a round trip.
//
// A host the index does not hold is in neither set. That is the backend
// intersecting a declared target with the business's hosts: a host in the list
// that CMDB has never heard of is not expected, and it has not left either,
// because it was never here.
func resolveNoDataHosts(
	index execution.HostBusiness, businessID string, candidates []nodata.HostCandidate,
) nodata.HostResolution {
	resolution := nodata.HostResolution{
		// Asked once for the whole pass rather than per host: it is a fact
		// about this process, and reading it per host would let it change
		// halfway through one target's resolution.
		Resolved:      index.HostIndexResolved(),
		Known:         make(map[string]struct{}, len(candidates)),
		OutOfBusiness: make(map[string]struct{}),
	}
	if !resolution.Resolved {
		// Nothing to intersect with. Walking the candidates would produce the
		// empty sets anyway, and a caller that reads them without reading
		// Resolved would see a target that resolved to nobody; returning here
		// is only to say that plainly.
		return resolution
	}
	for _, candidate := range candidates {
		business, held := index.LookupHostBusiness(candidate.Key())
		switch {
		case !held:
			continue
		case business == businessID:
			resolution.Known[candidate.Key()] = struct{}{}
		default:
			// Keyed by group, because that is what the evaluation asks about.
			resolution.OutOfBusiness[candidate.GroupKey] = struct{}{}
		}
	}
	return resolution
}
