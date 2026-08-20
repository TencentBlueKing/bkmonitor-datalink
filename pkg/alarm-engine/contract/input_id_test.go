// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "testing"

func TestDeriveInputIDUsesFrozenLengthPrefixedTuple(t *testing.T) {
	t.Parallel()

	got, err := DeriveInputID(InputIdentity{
		TenantID:              "default",
		Purpose:               PurposeDetect,
		StrategyID:            "1",
		ItemID:                "1",
		StrategyContentSHA256: "8a340c044a560d3410cd4d53098151eac966b8321a5ad01b43547b05f960e2c3",
		RecordID:              "55a76cf628e46c04a052f4e19bdb9dbf.1569246480",
	})
	if err != nil {
		t.Fatalf("DeriveInputID() error = %v", err)
	}

	const want = "2c82173befc4a450616df467cfd27d903ac5417e44413c7144780dfe43a8ef44"
	if got != want {
		t.Fatalf("DeriveInputID() = %q, want %q", got, want)
	}
}

func TestDeriveInputIDUsesUTF8ByteLength(t *testing.T) {
	t.Parallel()

	got, err := DeriveInputID(InputIdentity{
		TenantID:              "租户",
		Purpose:               PurposeDetect,
		StrategyID:            "1",
		ItemID:                "1",
		StrategyContentSHA256: "8a340c044a560d3410cd4d53098151eac966b8321a5ad01b43547b05f960e2c3",
		RecordID:              "55a76cf628e46c04a052f4e19bdb9dbf.1569246480",
	})
	if err != nil {
		t.Fatalf("DeriveInputID() error = %v", err)
	}
	const want = "4b25f5e001820e6de39c45a27bf65b27b665d542009d3a004a73ee1629f014d7"
	if got != want {
		t.Fatalf("DeriveInputID() = %q, want %q", got, want)
	}
}

func TestDeriveInputIDRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()

	_, err := DeriveInputID(InputIdentity{
		TenantID:              string([]byte{0xff}),
		Purpose:               PurposeDetect,
		StrategyID:            "1",
		ItemID:                "1",
		StrategyContentSHA256: "8a340c044a560d3410cd4d53098151eac966b8321a5ad01b43547b05f960e2c3",
		RecordID:              "55a76cf628e46c04a052f4e19bdb9dbf.1569246480",
	})
	if err == nil {
		t.Fatal("DeriveInputID() accepted invalid UTF-8")
	}
}

func TestDeriveInputIDRejectsNonCanonicalFields(t *testing.T) {
	t.Parallel()

	base := InputIdentity{
		TenantID:              "default",
		Purpose:               PurposeDetect,
		StrategyID:            "1",
		ItemID:                "1",
		StrategyContentSHA256: "8a340c044a560d3410cd4d53098151eac966b8321a5ad01b43547b05f960e2c3",
		RecordID:              "55a76cf628e46c04a052f4e19bdb9dbf.1569246480",
	}
	tests := map[string]func(*InputIdentity){
		"lowercase purpose": func(value *InputIdentity) { value.Purpose = "detect" },
		"leading zero id":   func(value *InputIdentity) { value.StrategyID = "01" },
		"uppercase hash": func(value *InputIdentity) {
			value.StrategyContentSHA256 = "8A340C044A560D3410CD4D53098151EAC966B8321A5AD01B43547B05F960E2C3"
		},
		"leading zero time": func(value *InputIdentity) {
			value.RecordID = "55a76cf628e46c04a052f4e19bdb9dbf.01569246480"
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := base
			mutate(&value)
			if _, err := DeriveInputID(value); err == nil {
				t.Fatal("DeriveInputID() error = nil, want validation error")
			}
		})
	}
}
