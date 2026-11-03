// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"reflect"
	"strings"
)

// UptimeKeys are the keys of a detect's uptime the compiler reads, which are
// the ones Python's in_alarm_time reads (alarm_backends/core/control/strategy.py).
func UptimeKeys() []string { return jsonKeys(reflect.TypeOf(uptimeConfigV1{})) }

// TraditionalComparisonKeys are the parameters the history-comparison
// algorithms read, which are the fields Python's serializers declare for them
// (bkmonitor/strategy/serializers.py).
func TraditionalComparisonKeys() []string {
	return jsonKeys(reflect.TypeOf(TraditionalComparisonParameters{}))
}

// UptimeSource, EffectiveTimeSnapshotSource and EffectiveTimeRepeatSource are
// what the compiler decodes the parts of a strategy document it is handed raw
// into: a detect's uptime, the effective-time snapshot, and the repeat rule
// each snapshot calendar item carries raw. The operator evidence walks them
// for the keys alarmd reads.
func UptimeSource() reflect.Type { return reflect.TypeOf(uptimeConfigV1{}) }

func EffectiveTimeSnapshotSource() reflect.Type { return reflect.TypeOf(effectiveSnapshot{}) }

func EffectiveTimeRepeatSource() reflect.Type { return reflect.TypeOf(effectiveRepeat{}) }

func jsonKeys(kind reflect.Type) []string {
	keys := make([]string, 0, kind.NumField())
	for index := 0; index < kind.NumField(); index++ {
		name, _, _ := strings.Cut(kind.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}
