// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestResourceInstanceQueryUsesHostIDPriority(t *testing.T) {
	t.Parallel()
	instID, _ := domain.NewNumberScalar(101)
	hostID, _ := domain.NewNumberScalar(102)
	targetID, _ := domain.NewNumberScalar(103)
	query, diagnostics := resourceInstanceQuery(rules.HostModelCode, domain.DimensionMap{
		"bk_inst_id": instID, "bk_host_id": hostID, "bk_target_host_id": targetID,
	})
	if len(diagnostics) != 0 || query.ModelCode != rules.HostModelCode || query.Filters["cw_object_model_inst_id"] != "101" {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}

func TestResourceInstanceQueryFallsBackToHostAddress(t *testing.T) {
	t.Parallel()
	cloudID, _ := domain.NewNumberScalar(0)
	query, diagnostics := resourceInstanceQuery(rules.HostModelCode, domain.DimensionMap{
		"bk_target_ip": domain.NewStringScalar("10.0.0.1"), "bk_target_cloud_id": cloudID,
	})
	if len(diagnostics) != 1 || query.Filters["bk_host_innerip"] != "10.0.0.1" || query.Filters["bk_cloud_id"] != float64(0) {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}

func TestResourceInstanceQueryKeepsNonHostBoundary(t *testing.T) {
	t.Parallel()
	hostID, _ := domain.NewNumberScalar(102)
	query, diagnostics := resourceInstanceQuery("cw-Disk", domain.DimensionMap{"bk_host_id": hostID})
	if len(query.Filters) != 0 || len(diagnostics) != 1 || diagnostics[0].Fields[0] != "dimensions.bk_inst_id" {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}
