// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package publicsurface

import (
	"net/url"
	"testing"
)

// The link resolves to the login page under the deployment's base from any
// refused path, whatever prefix the host routes the deployment under.
func TestTheLoginLinkResolvesUnderTheBaseFromAnyPath(t *testing.T) {
	for _, base := range []string{"https://ob.example/", "https://ob.example/prefix/deep/"} {
		for _, path := range []string{"/metrics", "/api/objects", "/api/objects/one", "/api/strategies/", "/api/a/b/c/d"} {
			baseURL, _ := url.Parse(base)
			refused := baseURL.ResolveReference(&url.URL{Path: path[1:]})
			login := refused.ResolveReference(&url.URL{Path: LoginHref(path)})
			if want := baseURL.ResolveReference(&url.URL{Path: LoginPage}); login.String() != want.String() {
				t.Errorf("from %s under %s the link %q reaches %s, want %s", path, base, LoginHref(path), login, want)
			}
		}
	}
}
