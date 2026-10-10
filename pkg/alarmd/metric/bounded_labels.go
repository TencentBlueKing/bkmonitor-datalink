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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// OutputRejectedStrategyLabels is how many strategies get a label of their
// own on output_events_rejected_total in one process. A refusal is a
// strategy's content breaking a converter rule, which a handful of
// strategies do; the bound is what keeps a converter bug that refuses every
// strategy from minting a series per strategy.
const OutputRejectedStrategyLabels = 64

// boundedLabels hands out a label value per identity, first come, up to a
// limit, and _other after it. An identity keeps the label it was given.
type boundedLabels struct {
	mu    sync.Mutex
	limit int
	seen  map[string]struct{}
}

// label is the value to use for id, and false when id was folded to _other
// because the limit was reached. An empty id folds without counting as
// overflow: it names nothing.
func (labels *boundedLabels) label(id string) (string, bool) {
	if id == "" || id == observability.OutputRejectOther {
		return observability.OutputRejectOther, true
	}
	labels.mu.Lock()
	defer labels.mu.Unlock()
	if _, known := labels.seen[id]; known {
		return id, true
	}
	if len(labels.seen) >= labels.limit {
		return observability.OutputRejectOther, false
	}
	if labels.seen == nil {
		labels.seen = make(map[string]struct{}, labels.limit)
	}
	labels.seen[id] = struct{}{}
	return id, true
}
