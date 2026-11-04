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
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A refused front read writes its code and operation once a period per pair,
// the lines held back counted on the next; another operation has its line at
// once; an operation name is written bounded, as the caller's text it is.
func TestARefusedFrontReadIsWrittenOncePerOperationAPeriod(t *testing.T) {
	var out bytes.Buffer
	at := time.Unix(1_800_000_000, 0)
	line := frontRefusedLine(observability.New("control", &out), func() time.Time { return at })
	line("op_not_permitted_for_front", "strategy.linkd_reconcile")
	line("op_not_permitted_for_front", "strategy.linkd_reconcile")
	line("op_not_permitted_for_front", "fleet.get")
	line("op_not_permitted_for_front", strings.Repeat("x", 200)+"\n")
	at = at.Add(frontRefusedEvery)
	line("op_not_permitted_for_front", "strategy.linkd_reconcile")
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var l map[string]any
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("line %q: %v", raw, err)
		}
		lines = append(lines, l)
	}
	if len(lines) != 4 || lines[0]["operation"] != "strategy.linkd_reconcile" || lines[1]["operation"] != "fleet.get" ||
		len(lines[2]["operation"].(string)) != frontOperationBytes || lines[3]["held_since_last"] != float64(1) {
		t.Fatalf("lines %v", lines)
	}
}
