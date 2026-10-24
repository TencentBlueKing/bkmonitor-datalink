// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"encoding/json"
	"testing"
)

// An APM record carries its application beside its identity dimensions (the
// query asks the provider for it without making it part of the identity).
// The alert fingerprint is the one the record had without it; the APM object
// is named from it, hyphens included, and reported as supplementary.
func TestACarriedAPMApplicationNamesTheObjectAndNotTheFingerprint(t *testing.T) {
	identity := MonitorOutputIdentity{DimensionFields: []string{"service_name"}}
	plain := map[string]json.RawMessage{"service_name": json.RawMessage(`"transfer"`)}
	carried := map[string]json.RawMessage{"service_name": json.RawMessage(`"transfer"`), "app_name": json.RawMessage(`"cross-bank-trade"`)}
	a, err := MonitorDedupeMD5("131", "2", plain, identity)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MonitorDedupeMD5("131", "2", carried, identity)
	if err != nil || a != b {
		t.Fatalf("fingerprint moved: %s vs %s (%v)", a, b, err)
	}
	subject, remaining, err := ProjectMonitorSubject(carried, identity, &MonitorSubjectFacts{ResultTableID: "2_bkapm_metric_cross_bank_trade.__default__"})
	if err != nil || subject.Type != MonitorSubjectAPMService || subject.ID != "cross-bank-trade:transfer" ||
		string(subject.Additional["app_name"]) != `"cross-bank-trade"` || subject.Additional["service_name"] != nil {
		t.Fatalf("subject %+v (%v), want cross-bank-trade:transfer with app_name supplementary", subject, err)
	}
	if _, kept := remaining["app_name"]; kept {
		t.Fatalf("remaining dimensions %v, want the identity dimensions only", remaining)
	}
	// Without it, and with nothing else naming the application, no object.
	if subject, _, err := ProjectMonitorSubject(plain, identity, &MonitorSubjectFacts{ResultTableID: "2_bkapm_metric_cross_bank_trade.__default__"}); err != nil || subject.Type != "" {
		t.Fatalf("no application: %+v (%v), want no object", subject, err)
	}
}
