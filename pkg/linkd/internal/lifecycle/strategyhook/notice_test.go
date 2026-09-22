// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategyhook

import (
	"testing"

	"linkd/internal/activeindex"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

func TestHookOnlyQueuesRefresh(t *testing.T) {
	c := &setClient{}
	h, err := New(c, Config{KeyPrefix: "active", Timeout: 1000000000})
	if err != nil {
		t.Fatal(err)
	}
	input := hookInput()
	first, err := h.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Execute(t.Context(), input)
	if err != nil || first.MessageID != again.MessageID {
		t.Fatal("unstable invocation identity")
	}
	for _, v := range []lifecycle.FinalHookInput{terminal(input, domain.AlertStatusClosed), input, terminal(input, domain.AlertStatusRecovered)} {
		if _, err := h.Execute(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}
	pending := c.values[activeindex.HintKeys("active")[0]]
	if len(c.values) != 1 || len(pending) != 1 || !pending["active:tenant:123"] {
		t.Fatalf("hook must only merge refresh hints: %+v", c.values)
	}
	input.Alert.BKTenantID = "other"
	if _, err := h.Execute(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if !pending["active:other:123"] {
		t.Fatal("tenant not isolated")
	}
}
