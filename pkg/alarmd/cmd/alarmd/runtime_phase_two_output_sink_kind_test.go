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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A write refused because the sink is not open says so by its kind: no
// broker was asked, though its completion reason is the shared
// OUTPUT_ACK_UNKNOWN, since the Slot is done again either way.
func TestAWriteToASinkNotOpenSaysNoBrokerWasAsked(t *testing.T) {
	err := fmt.Errorf("alarmd worker: acknowledge events: %w", &outputSinkNotOpenError{})
	if got := observability.OutputFailureKindOf(err); got != observability.OutputFailureSinkNotOpen {
		t.Fatalf("kind %q, want sink_not_open", got)
	}
}
