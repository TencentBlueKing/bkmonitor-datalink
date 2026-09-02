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
	"strings"
	"testing"
)

func TestRuntimeIdentityKeySupportsNegativeBusinessID(t *testing.T) {
	identity := RuntimeIdentity{
		TenantID:                "tenant-a",
		BusinessID:              "-42",
		StrategyID:              "1001",
		StateCompatibilityHash:  strings.Repeat("a", 64),
		DimensionIdentityDigest: strings.Repeat("b", 64),
	}

	key, err := identity.Key("alarmd-shadow")
	if err != nil {
		t.Fatalf("Key() error = %v", err)
	}
	if !strings.HasPrefix(key, "alarmd-shadow:runtime:v1:w:") {
		t.Fatalf("Key() = %q, want configured v1 prefix", key)
	}
	if !strings.Contains(key, ":-42:1001:") {
		t.Fatalf("Key() = %q, want explicit signed business and strategy identities", key)
	}
	if strings.Contains(key, identity.TenantID) {
		t.Fatalf("Key() = %q, tenant must be represented by a bounded digest", key)
	}
	if got, want := len(strings.Split(key, ":")), 9; got != want {
		t.Fatalf("Key() parts = %d, want %d: %q", got, want, key)
	}

	second, err := identity.Key("alarmd-shadow")
	if err != nil || second != key {
		t.Fatalf("Key() is not deterministic: second=%q err=%v", second, err)
	}
}

func TestRuntimeIdentityKeySeparatesStableIdentityFields(t *testing.T) {
	base := RuntimeIdentity{
		TenantID:                "tenant-a",
		BusinessID:              "2",
		StrategyID:              "1001",
		StateCompatibilityHash:  strings.Repeat("a", 64),
		DimensionIdentityDigest: strings.Repeat("b", 64),
	}
	baseKey, err := base.Key("alarmd")
	if err != nil {
		t.Fatalf("Key(base) error = %v", err)
	}

	cases := map[string]RuntimeIdentity{
		"tenant":        withTenant(base, "tenant-b"),
		"business":      withBusiness(base, "-2"),
		"strategy":      withStrategy(base, "1002"),
		"compatibility": withCompatibility(base, strings.Repeat("c", 64)),
		"dimension":     withDimension(base, strings.Repeat("d", 64)),
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			key, candidateErr := candidate.Key("alarmd")
			if candidateErr != nil {
				t.Fatalf("Key() error = %v", candidateErr)
			}
			if key == baseKey {
				t.Fatalf("Key() did not separate %s", name)
			}
		})
	}
}

func TestRuntimeIdentityRejectsNonCanonicalFields(t *testing.T) {
	valid := RuntimeIdentity{
		TenantID:                "tenant-a",
		BusinessID:              "2",
		StrategyID:              "1001",
		StateCompatibilityHash:  strings.Repeat("a", 64),
		DimensionIdentityDigest: strings.Repeat("b", 64),
	}
	cases := map[string]RuntimeIdentity{
		"empty tenant":           withTenant(valid, ""),
		"business plus":          withBusiness(valid, "+2"),
		"business negative zero": withBusiness(valid, "-0"),
		"business leading zero":  withBusiness(valid, "02"),
		"strategy negative":      withStrategy(valid, "-1"),
		"strategy leading zero":  withStrategy(valid, "01"),
		"compatibility digest":   withCompatibility(valid, strings.Repeat("A", 64)),
		"dimension digest":       withDimension(valid, "abc"),
	}
	for name, identity := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := identity.Key("alarmd"); err == nil {
				t.Fatal("Key() error = nil, want validation error")
			}
		})
	}
	if _, err := valid.Key(""); err == nil {
		t.Fatal("Key(empty prefix) error = nil")
	}
}

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

func withTenant(identity RuntimeIdentity, value string) RuntimeIdentity {
	identity.TenantID = value
	return identity
}

func withBusiness(identity RuntimeIdentity, value string) RuntimeIdentity {
	identity.BusinessID = value
	return identity
}

func withStrategy(identity RuntimeIdentity, value string) RuntimeIdentity {
	identity.StrategyID = value
	return identity
}

func withCompatibility(identity RuntimeIdentity, value string) RuntimeIdentity {
	identity.StateCompatibilityHash = value
	return identity
}

func withDimension(identity RuntimeIdentity, value string) RuntimeIdentity {
	identity.DimensionIdentityDigest = value
	return identity
}
