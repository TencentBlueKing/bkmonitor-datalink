// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"encoding/json"
	"testing"
)

func TestResultTableDetailIgnoresSurrealDBSubRoute(t *testing.T) {
	payload := []byte(`{"storage_id":6,"storage_name":"vm-main","storage_type":"victoria_metrics","vm_rt":"2_graph_metric_vm","surrealdb":{"storage_id":7,"storage_type":"surrealdb","database":"2_graph_rt","namespace":"mapleleaf_2"}}`)
	var detail ResultTableDetail

	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatalf("unmarshal result table detail: %v", err)
	}
	if detail.StorageId != 6 {
		t.Fatalf("unexpected storage id: %d", detail.StorageId)
	}
	if detail.StorageType != "victoria_metrics" {
		t.Fatalf("unexpected storage type: %s", detail.StorageType)
	}
	if detail.StorageName != "vm-main" {
		t.Fatalf("unexpected storage name: %s", detail.StorageName)
	}
	if detail.VmRt != "2_graph_metric_vm" {
		t.Fatalf("unexpected vm rt: %s", detail.VmRt)
	}
}
