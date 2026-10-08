// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourcereads_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/sourcereads"
)

// The list is only as good as its reach. One key from each way it finds
// them must be on it, each a key that no other way lists too, so a way that
// stopped reaching would be missed here; a walk that stopped short would hold
// the source view to less than alarmd reads, and the view's own test would
// pass on the shorter list.
func TestThePathsReachEveryWayAKeyIsRead(t *testing.T) {
	listed := map[string]bool{}
	for _, path := range sourcereads.Paths() {
		listed[path] = true
	}
	for _, want := range []string{
		"detects.trigger_config.check_window",                    // a nested struct inside a slice
		"items.query_configs.filter_dict",                        // a raw value decoded into a struct
		"items.query_configs.agg_condition.key",                  // a struct nested in one decoded later
		"items.query_configs.target_identity.object_model_field", // a second struct decoded from one raw value
		"items.target.value.bk_target_ip",                        // a value decoded by a custom unmarshal
		"effective_time_snapshot.business_timezone",              // a compiler struct for a value kept raw
		"effective_time_snapshot.calendars.items.repeat.freq",    // a raw value inside one, decoded by the compiler
		"items.algorithms.config.fetch_type",                     // the comparison parameters' key list
		"items.target_plan.static_targets.match.namespace",       // the target plan protocol's key tables
		"items.name", // a struct of another package reading the frozen copy
		"space_uid",  // a struct declared inside a function
		"items.target.value.cw_object_model_inst_id", // a key read from a map by name
		// Read by a struct declared inside a function (itemUnit) and by
		// the threshold processor's.
		"items.query_configs.unit",
	} {
		if !listed[want] {
			t.Errorf("%s is not listed; the list stops short of a way a key is read", want)
		}
	}
}
