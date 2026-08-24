// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"

// ComparatorDiagnostics reports bounded Shadow coverage loss after the
// corresponding broker acknowledgement. Callbacks must not retain payloads.
type ComparatorDiagnostics struct {
	OnCapacityDrop    func(ComparatorCapacityDrop)
	OnCoverageRelease func(ComparatorCoverageRelease)
	OnEpochRollover   func(ComparatorEpochRollover)
}

type ComparatorCapacityDrop struct {
	Role       comparator.StreamRole
	Partition  int32
	Offset     int64
	Dropped    int
	MaxEntries int
}

type ComparatorCoverageRelease struct {
	Entries       int
	Authoritative int
	Orphans       int
	MissingInput  int
	MissingGo     int
	MissingPython int
}

type ComparatorEpochRollover struct {
	Entries       int
	Authoritative int
}

func (d ComparatorDiagnostics) capacityDrop(event ComparatorCapacityDrop) {
	if d.OnCapacityDrop != nil {
		d.OnCapacityDrop(event)
	}
}

func (d ComparatorDiagnostics) coverageRelease(event ComparatorCoverageRelease) {
	if d.OnCoverageRelease != nil {
		d.OnCoverageRelease(event)
	}
}

func (d ComparatorDiagnostics) epochRollover(event ComparatorEpochRollover) {
	if d.OnEpochRollover != nil {
		d.OnEpochRollover(event)
	}
}
