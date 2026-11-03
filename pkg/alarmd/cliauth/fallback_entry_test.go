// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cliauth

import (
	"slices"
	"testing"
)

// A session issued through the admin is told the deployment's own address
// as its fallback entry, on the exchange and on every renewal: the CLI keeps
// reaching alarmd when the admin cannot. A session issued here already uses
// that address and is told none.
func TestASessionIssuedThroughTheAdminIsToldTheDeploymentsOwnEntry(t *testing.T) {
	m := &Manager{environmentID: "env", environmentName: "Env", publicBaseURL: "https://ops.example/ops-alarmd/", origin: "https://ops.example"}
	front := storedRecord{SessionID: "s1", Scope: ScopeReadonly, IssuedVia: IssuedViaFront, PublicBaseURL: "https://ops.example/ops-admin/alarmd/"}
	for name, refresh := range map[string]string{"exchange": "", "renewal": "next-refresh"} {
		got := m.login(front, "token", refresh)
		if got.PublicBaseURL != front.PublicBaseURL || !slices.Equal(got.FallbackBaseURLs, []string{m.publicBaseURL}) {
			t.Errorf("%s through the admin: entry %q fallback %v, want the admin entry and the deployment's own", name, got.PublicBaseURL, got.FallbackBaseURLs)
		}
	}
	direct := storedRecord{SessionID: "s2", Scope: ScopeReadonly, IssuedVia: IssuedViaAdminKey}
	if got := m.login(direct, "token", ""); got.PublicBaseURL != m.publicBaseURL || got.FallbackBaseURLs != nil {
		t.Errorf("issued here: entry %q fallback %v, want the deployment's own and no fallback", got.PublicBaseURL, got.FallbackBaseURLs)
	}
}

// The same rule names the fallback on a channel answer's session as on a
// login answer: the deployment's own address for an admin-issued entry,
// none for a session issued at that address or with no entry recorded.
func TestTheFallbackEntryRuleIsOneForLoginAndChannel(t *testing.T) {
	m := &Manager{publicBaseURL: "https://ops.example/ops-alarmd/", origin: "https://ops.example"}
	if got := m.fallbackFor("https://ops.example/ops-admin/alarmd/"); !slices.Equal(got, []string{m.publicBaseURL}) {
		t.Errorf("admin entry: %v", got)
	}
	// The fallback shares the admin's origin; an admin entry on another
	// host, scheme or port is offered nothing.
	for _, entry := range []string{"", m.publicBaseURL, "https://admin.example/ops-admin/alarmd/", "http://ops.example/ops-admin/alarmd/", "https://ops.example:8443/ops-admin/alarmd/"} {
		if got := m.fallbackFor(entry); got != nil {
			t.Errorf("entry %q: %v, want none", entry, got)
		}
	}
}
