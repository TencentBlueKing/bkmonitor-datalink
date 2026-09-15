// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "testing"

func TestRuntimeLevelContractIndexPreservesFirstMatch(t *testing.T) {
	refs := []RuntimeLevelContractRef{{LevelID: 9, DetectFingerprint: "first"}, {LevelID: 9, DetectFingerprint: "later"}, {LevelID: 3, DetectFingerprint: "dynamic"}}
	index := indexRuntimeLevelContracts(refs)
	if index[9].DetectFingerprint != "first" || index[3].DetectFingerprint != "dynamic" {
		t.Fatalf("index=%v", index)
	}
	if _, found := index[99]; found {
		t.Fatal("invented missing level")
	}
	refs[0].DetectFingerprint = "changed"
	if index[9].DetectFingerprint != "first" {
		t.Fatal("caller mutation changed index")
	}
}
