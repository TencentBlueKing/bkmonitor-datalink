// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kaccompat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestProjectionAcceptsMissingBusinessOnCreateAndStateUpdate(t *testing.T) {
	client := &Client{level: func(level string) (string, error) { return level, nil }}
	for _, raw := range []string{"", "null", `""`, "0"} {
		for _, state := range []string{"active", "shielded", "pending_merge"} {
			t.Run(raw+"/"+state, func(t *testing.T) {
				alert := storetest.Alert("tenant", "alert", "event", "fingerprint", "warning")
				if raw != "" {
					alert.ExtraData["bk_biz_id"] = json.RawMessage(raw)
				}
				if state == "shielded" {
					next := alert.UpdateAt.Add(time.Minute)
					alert.Shield = domain.AlertShield{Active: true, NextCheckAt: &next, Bindings: []domain.ShieldBinding{{BindingID: strings.Repeat("a", 64), ActivationID: strings.Repeat("b", 64), Policy: domain.PolicyVersion{ID: "shield", Version: 1, Digest: strings.Repeat("c", 64)}, Type: "time_shield", SourceEventID: alert.TriggerEventID, Severity: alert.Severity, BoundAt: alert.UpdateAt}}}
				}
				if state == "pending_merge" {
					alert.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: strings.Repeat("a", 64), GroupKey: strings.Repeat("c", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, MemberEventID: alert.TriggerEventID, Severity: alert.Severity, Groups: []int{0}, StartedAt: alert.UpdateAt, Deadline: alert.UpdateAt.Add(time.Minute)}}}
				}
				for _, create := range []bool{true, false} {
					fields, _, err := client.fields(t.Context(), alert, create)
					if err != nil {
						t.Fatal(err)
					}
					if create {
						if raw == "0" {
							if fields["bk_biz_id"] != int64(0) {
								t.Fatal(fields["bk_biz_id"])
							}
						} else if fields["bk_biz_id"] != nil {
							t.Fatal("missing business must remain null", fields["bk_biz_id"])
						}
					}
					if _, err = json.Marshal(fields); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
