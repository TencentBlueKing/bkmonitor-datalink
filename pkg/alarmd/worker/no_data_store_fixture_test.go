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
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// emptyNoDataStore answers every load with "this Plan has written nothing".
//
// It is what the fixtures of tests about something else use, so that the
// no-data port being required does not force each of them to have an opinion
// about no-data. A test that is about no-data uses a store that holds records.
type emptyNoDataStore struct {
	loads   int
	applied []execution.PlanNoDataMutation
}

func (store *emptyNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	store.loads++
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.NoDataMemorySnapshot{
			// NONE rather than an unset field: the store stamps it, and a
			// double that leaves it empty is standing in for a snapshot the
			// store cannot produce.
			Identity: item.Identity, Status: execution.NoDataMemoryMissing,
			Representation: execution.NoDataRepresentationNone,
		}
	}
	return result, nil
}

func (store *emptyNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		store.applied = append(store.applied, mutation)
		result.Items[index] = execution.NoDataApplyItemResult{
			Identity: mutation.Identity, Status: execution.NoDataApplied,
		}
	}
	return result, nil
}

// SharedNoDataStore is the one the fixtures of unrelated tests point at. Its
// counters are not read by those tests; a test that reads them makes its own.
var SharedNoDataStore = &emptyNoDataStore{}

// fixedHostBusiness answers from a map, which is what the CMDB index is.
type fixedHostBusiness struct {
	byIdentity map[string]string
}

func (lookup fixedHostBusiness) LookupHostBusiness(identity string) (string, bool) {
	business, held := lookup.byIdentity[identity]
	return business, held
}

// HostIndexResolved follows the index this fake stands for: one holding hosts
// is an index, one holding none is a process that has not built one. Tying it
// to the map rather than to a flag of its own is what keeps a fixture from
// claiming to have resolved a target out of an index with nothing in it.
func (lookup fixedHostBusiness) HostIndexResolved() bool { return len(lookup.byIdentity) > 0 }

// SharedHostBusiness holds no host, which is what a deployment whose CMDB index
// has not been built yet looks like. Tests about something else use it; a test
// about host resolution builds its own.
var SharedHostBusiness = fixedHostBusiness{}
