// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import "testing"

func TestKACOperationKinds(t *testing.T) {
	for _, value := range []OperationKind{"not_dispatched", "dispatched", "response", "pending_execute", "approval", "auto_executing", "auto_executing_failure", "auto_executing_retry", "auto_executing_success", "auto_ordering", "auto_ordering_failure", "auto_ordering_finish", "manual_closed", "auto_closed", "system_closed", "source_closed", "restored", "notify"} {
		if !value.Valid() {
			t.Errorf("unsupported KAC operation %q", value)
		}
	}
	// 这些值只属于 KAC 派生展示或旧索引，新追加协议不再登记。
	for _, value := range []OperationKind{"received", "enrich", "pending_merge", "convergence", "merged_into", "merged", "shielded", "claim", "auto_ordering_success", "feedback", "ast_match_shadow"} {
		if value.Valid() {
			t.Errorf("non-writable KAC operation registered: %q", value)
		}
	}
}
