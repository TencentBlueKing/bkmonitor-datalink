// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package publicsurface is what the query listener answers for a route it no
// longer serves in public.
//
// The public surface is restricted exactly when the process holds a deployment
// administrator key (config.Config.PublicSurfaceRestricted). Holding the key is
// what makes the CLI session a working second way in, so only then can the
// routes that carry deployment coordinates move behind it; a deployment without
// the key has no other way to read them and keeps the surface it always had.
package publicsurface

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RestrictedCode names the refusal, so a reader tells "moved behind the CLI"
// from "not found" or "not ready".
const RestrictedCode = "public_surface_restricted"

// LoginPage is the login page's path under the deployment's own address. It
// is relative: the deployment is served under whatever prefix its host
// routes, and a coordinate is exactly what a refusal must not carry.
const LoginPage = "cli"

// Refuse answers a restricted route. The answer names where the data went,
// and the way in -- the login page, as a link that resolves from the
// refused path and relative to the deployment's base -- and nothing about
// the data.
func Refuse(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(response).Encode(map[string]any{"status": "error", "error": map[string]string{
		"code":       RestrictedCode,
		"message":    "This deployment serves this route only inside an alarmd-cli session; the public surface carries the page, a summary without deployment coordinates, observation windows and the CLI endpoints. Log in with alarmd-cli login, through the login page.",
		"login_page": LoginPage,
		"login_href": LoginHref(request.URL.Path),
	}})
}

// LoginHref is the login page as a link relative to path: one step up for
// every directory the path sits in, so it resolves to the page under the
// deployment's base whatever prefix the host adds in front.
func LoginHref(path string) string {
	depth := strings.Count(path, "/") - 1
	if depth < 0 {
		depth = 0
	}
	return strings.Repeat("../", depth) + LoginPage
}
