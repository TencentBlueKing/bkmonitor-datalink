// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"encoding/json"
	"os"
	"testing"
)

// Every operation the admin page calls through the front surface is one
// FrontReads serves. The page's list (testdata/admin_front_reads.json, a
// snapshot of the admin's FRONT_READ_OPERATIONS with its source commit) is
// the other side of the contract: when 316827163 dropped
// strategy.linkd_reconcile from FrontReads the page read blank with a 403 and
// nothing failed. A change to FrontReads that drops an operation the
// page calls fails here. Nothing compares the snapshot with the admin's list
// itself: the change that adds an operation to FRONT_READ_OPERATIONS owns
// refreshing this snapshot, and the FrontReads entry it needs, in datalink.
func TestEveryOperationTheAdminPageCallsIsAFrontRead(t *testing.T) {
	payload, err := os.ReadFile("testdata/admin_front_reads.json")
	if err != nil {
		t.Fatal(err)
	}
	var admin struct {
		Source     string   `json:"source"`
		Operations []string `json:"operations"`
	}
	if err := json.Unmarshal(payload, &admin); err != nil {
		t.Fatal(err)
	}
	if admin.Source == "" || len(admin.Operations) == 0 {
		t.Fatalf("the snapshot names no source or no operation: %+v", admin)
	}
	for _, operation := range admin.Operations {
		if !FrontReads[operation] {
			t.Errorf("the admin page calls %s (%s) and the front surface does not serve it: the page would read blank", operation, admin.Source)
		}
	}
}
