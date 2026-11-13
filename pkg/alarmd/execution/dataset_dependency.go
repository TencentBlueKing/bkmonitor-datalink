// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// WithoutDimensions is the Dataset as a query that feeds no PRIMARY input
// keeps it (decision-020 batch 8, the dependency input): each columnar run
// with its series' identity digest, its times and values and its record ids,
// and neither its dimensions nor its identity's fields - a dependency's
// readers (the detectors' history, the binding's checks, the algorithm-input
// facts) read the digest, the times and the values, and the dimensions a
// series is alerted and grouped by are its PRIMARY's. A run kept as whole
// records is kept as it is. The Dataset it was made from is left as it was.
func (dataset *Dataset) WithoutDimensions() *Dataset {
	if dataset == nil {
		return nil
	}
	slim := &Dataset{len: dataset.len, starts: append([]int(nil), dataset.starts...),
		runs: make([]datasetRun, len(dataset.runs))}
	for index := range dataset.runs {
		from, to := &dataset.runs[index], &slim.runs[index]
		if from.records != nil {
			to.records = from.records
			continue
		}
		// Field by field: a run holds an atomic and is never copied whole.
		to.business, to.received, to.valueField = from.business, from.received, from.valueField
		to.identity.Digest = from.identity.Digest
		to.ids, to.idWidth, to.derived = from.ids, from.idWidth, from.derived
		to.times, to.first, to.step, to.values = from.times, from.first, from.step, from.values
		to.sideMarks, to.side = from.sideMarks, from.side
	}
	return slim
}
