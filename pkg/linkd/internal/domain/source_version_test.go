// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSourceVersionRedeliveryPreservesPersistedFacts(t *testing.T) {
	now := time.Now().UTC()
	original := Event{EventSourceVersion: 1, BKTenantID: "tenant", EventSourceID: "source", EventID: "event", ReceivedAt: now, SourceRawData: JSONObject{"severity": json.RawMessage(`"P1"`)}}
	incoming := original.Clone()
	incoming.EventSourceVersion = 2
	incoming.Severity = "critical"
	if e := ValidateEventRedelivery(incoming, original); e != nil {
		t.Fatal(e)
	}
	incoming.SourceRawData["severity"] = json.RawMessage(`"P2"`)
	if e := ValidateEventRedelivery(incoming, original); e == nil {
		t.Fatal("different raw facts accepted")
	}
}

func TestSourceVersionsMustBePositive(t *testing.T) {
	if e := (Event{}).Validate(); e == nil {
		t.Fatal("zero event version accepted")
	}
	if e := (Alert{}).Validate(); e == nil {
		t.Fatal("zero alert version accepted")
	}
}
