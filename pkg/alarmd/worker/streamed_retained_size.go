// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The facts a shared binding carries (sharedBindingHeldBytes) and a view.
var (
	qualityFactBytes     = uint64(unsafe.Sizeof(execution.InputQualityFact{}))
	inputTerminalBytes   = uint64(unsafe.Sizeof(execution.InputTerminal{}))
	partialEvidenceBytes = uint64(unsafe.Sizeof(execution.PartialEvidence{}))
	datasetViewBytes     = uint64(unsafe.Sizeof(execution.DatasetView{}))
)

// streamedRetainedSize is what a series batch holds while its Slot keeps it,
// as the structure says (no encoding, no read of the runtime): each binding's
// entry in the streamed map (streamedEntryBytes), each Dataset and each view
// the batch and its bindings point at, once. What its bindings share with
// every other series of their query is charged once, when it is first kept
// (ConsumeSeries, sharedBindingHeldBytes).
func streamedRetainedSize(batch execution.SeriesExecutionBatch) uint64 {
	total := batch.Dataset.RetainedBytes()
	counted := []*execution.Dataset{batch.Dataset}
	var views []*execution.DatasetView
	for index := range batch.Inputs {
		binding := &batch.Inputs[index]
		total += streamedEntryBytes
		if binding.View != nil && !viewAt(views, binding.View) {
			views = append(views, binding.View)
			total += datasetViewBytes + 4*uint64(binding.View.Len())
		}
		if binding.Dataset != nil && !pointsAt(counted, binding.Dataset) {
			counted = append(counted, binding.Dataset)
			total += binding.Dataset.RetainedBytes()
		}
	}
	return total
}

func viewAt(views []*execution.DatasetView, view *execution.DatasetView) bool {
	for _, counted := range views {
		if counted == view {
			return true
		}
	}
	return false
}

func pointsAt(datasets []*execution.Dataset, dataset *execution.Dataset) bool {
	for _, counted := range datasets {
		if counted == dataset {
			return true
		}
	}
	return false
}
