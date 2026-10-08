// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"strings"
	"testing"

	"linkd/internal/domain"
)

func TestMergeDecisionIdentityIsTenantAndWindowScoped(t *testing.T) {
	window := strings.Repeat("a", 64)
	first, err := domain.MergeDecisionID("tenant", window)
	if err != nil || len(first) != 64 {
		t.Fatal(err)
	}
	again, err := domain.MergeDecisionID("tenant", window)
	if err != nil || again != first {
		t.Fatal("identity unstable", err)
	}
	other, err := domain.MergeDecisionID("other", window)
	if err != nil || other == first {
		t.Fatal("tenant identity overlap", err)
	}
	next, err := domain.MergeDecisionID("tenant", strings.Repeat("b", 64))
	if err != nil || next == first {
		t.Fatal("window identity overlap", err)
	}
	for _, v := range []struct{ tenant, window string }{{"", window}, {"tenant", "bad"}, {"tenant", strings.ToUpper(window)}} {
		if _, err := domain.MergeDecisionID(v.tenant, v.window); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}
