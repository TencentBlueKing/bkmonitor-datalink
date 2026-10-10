// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A refusal's text is cut at its bound on a character, never through one:
// the text can quote a document in any language, and half a character
// reaches the reader as a replacement mark.
func TestDispositionDetailIsCutOnACharacter(t *testing.T) {
	short := "查询配置无效"
	if got := dispositionDetail(short); got != short {
		t.Fatalf("a text under the bound was changed: %q", got)
	}
	// Two bytes before three-byte characters put the bound inside one.
	long := "ab" + strings.Repeat("写", dispositionDetailMaxBytes)
	if utf8.RuneStart(long[dispositionDetailMaxBytes]) {
		t.Fatal("the fixture no longer puts the bound inside a character")
	}
	got := dispositionDetail(long)
	if !utf8.ValidString(got) || len(got) > dispositionDetailMaxBytes || !strings.HasPrefix(long, got) {
		t.Fatalf("cut = %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if len(got) < dispositionDetailMaxBytes-utf8.UTFMax {
		t.Fatalf("cut %d bytes short of the bound %d", dispositionDetailMaxBytes-len(got), dispositionDetailMaxBytes)
	}
}
