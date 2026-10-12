// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"testing"
)

func TestRuntimeStateSemanticsAreStableAndM0Compatible(t *testing.T) {
	semantics, err := RuntimeStateSemantics()
	if err != nil {
		t.Fatalf("RuntimeStateSemantics() error = %v", err)
	}
	if semantics.StateSchemaVersion == "" || semantics.CodecSemanticsVersion == "" ||
		semantics.SourceTimeSemanticsVersion == "" || semantics.HistoryCellSemanticsVersion == "" {
		t.Fatalf("RuntimeStateSemantics() contains empty version: %+v", semantics)
	}
	if len(semantics.IdentitySchemaDigest) != 64 {
		t.Fatalf("IdentitySchemaDigest length = %d, want 64", len(semantics.IdentitySchemaDigest))
	}
	second, err := RuntimeStateSemantics()
	if err != nil || second != semantics {
		t.Fatalf("RuntimeStateSemantics() is not deterministic: second=%+v err=%v", second, err)
	}
}
