// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"strings"
	"testing"

	linkdconfig "linkd/internal/config"
)

func TestResolveSourceAndTenant(t *testing.T) {
	t.Parallel()

	base := testSource()
	tests := []struct {
		name       string
		source     linkdconfig.EventSource
		sourceID   string
		tenantID   string
		wantTenant string
		wantError  string
	}{
		{name: "message tenant", source: base, sourceID: base.EventSourceID, tenantID: "tenant-a", wantTenant: "tenant-a"},
		{
			name: "related tenant", source: func() linkdconfig.EventSource {
				source := base
				source.RelatedTenantID = "tenant-fixed"
				return source
			}(),
			sourceID: base.EventSourceID, wantTenant: "tenant-fixed",
		},
		{
			name: "related tenant mismatch", source: func() linkdconfig.EventSource {
				source := base
				source.RelatedTenantID = "tenant-fixed"
				return source
			}(),
			sourceID: base.EventSourceID, tenantID: "tenant-other", wantError: "does not match",
		},
		{name: "tenant required", source: base, sourceID: base.EventSourceID, wantError: "tenant_id is required"},
		{
			name: "disabled", source: func() linkdconfig.EventSource {
				source := base
				source.Enabled = false
				return source
			}(),
			sourceID: base.EventSourceID, tenantID: "tenant-a", wantError: "disabled",
		},
		{name: "missing", source: base, sourceID: "missing", tenantID: "tenant-a", wantError: "was not found"},
		{
			name: "non unique fingerprint", source: func() linkdconfig.EventSource {
				source := base
				source.FingerprintField = "condition_key"
				return source
			}(),
			sourceID: base.EventSourceID, tenantID: "tenant-a", wantError: "fingerprint must include",
		},
		{
			name: "composite unique fingerprint", source: func() linkdconfig.EventSource {
				source := base
				source.FingerprintMode = linkdconfig.FingerprintModeFields
				source.FingerprintField = ""
				source.FingerprintFields = []string{"condition_key", "dimensions.generator_id"}
				return source
			}(),
			sourceID: base.EventSourceID, tenantID: "tenant-a", wantTenant: "tenant-a",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := linkdconfig.Default()
			cfg.EventSources = []linkdconfig.EventSource{test.source}
			_, tenant, err := ResolveSource(cfg, test.sourceID, test.tenantID)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("ResolveSource() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSource() error = %v", err)
			}
			if tenant != test.wantTenant {
				t.Fatalf("ResolveSource() tenant = %q, want %q", tenant, test.wantTenant)
			}
		})
	}
}

func TestGeneratedRunIdentity(t *testing.T) {
	t.Parallel()
	first, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !runIDPattern.MatchString(first) || len(first) != 24 {
		t.Fatalf("generated run IDs = %q and %q", first, second)
	}
	seed, err := ResolveSeed(0)
	if err != nil || seed == 0 {
		t.Fatalf("ResolveSeed(0) = %d, %v", seed, err)
	}
	if seed, err = ResolveSeed(42); err != nil || seed != 42 {
		t.Fatalf("ResolveSeed(42) = %d, %v", seed, err)
	}
}
