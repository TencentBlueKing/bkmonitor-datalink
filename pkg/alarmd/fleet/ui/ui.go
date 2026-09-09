// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package ui serves the object page alarmd carries with it.
//
// The page is part of this binary rather than a host's frontend, so a host
// embeds it instead of reimplementing it: the second host to show these facts
// costs one proxy route and one iframe, not a second port of the page. It
// follows that the page cannot know where it is mounted, so every request it
// makes is relative to its own location and this handler must work under any
// prefix.
package ui

import (
	_ "embed"
	"net/http"
	"strings"
	"time"
)

//go:embed index.html
var page []byte

// Modified is the timestamp served for caching. Build time is not available
// here, so a fixed instant is used: the page changes only when the binary does,
// and the binary's own version is what an operator checks.
var modified = time.Unix(0, 0)

// Handler serves the page at the root of whatever it is mounted under.
//
// Only the page itself is served. A host that proxies a prefix here will send
// every unmatched path through, and answering all of them with the page would
// turn a typo in an API path into a silent HTML body that the caller parses as
// JSON and reports as a decode error rather than as a 404.
func Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if path := strings.TrimSuffix(request.URL.Path, "/"); path != "" && path != "/index.html" {
			http.NotFound(response, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		// No frame-busting header: being embedded by a host is the delivery
		// model, and a host that must not be framed enforces that on its own
		// route rather than here.
		http.ServeContent(response, request, "index.html", modified, strings.NewReader(string(page)))
	})
}
