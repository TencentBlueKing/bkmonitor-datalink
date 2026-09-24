// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// A deployment whose public surface is not restricted serves /api/diagnose
// to whoever reaches it, and some read it that way and never through the
// CLI. WithDeploymentSection gives that route's first page the same
// deployment section the CLI's diagnosis adds, so a finding such as
// REDIS_MEMORY_NEAR_LIMIT is read there too.
//
// The section is spliced in before the page's closing brace: every byte the
// page had is sent as it was, and the page gains one key. It is never
// installed on a restricted surface, which refuses the route before it
// reaches this handler.

// WithDeploymentSection serves next and adds "deployment" to the first page
// of GET /api/diagnose. A later page, a refusal, a request a follower
// forwarded to this Leader -- the follower adds the section itself -- and
// anything that is not a JSON object answered 200 pass through untouched.
func WithDeploymentSection(next http.Handler, reads []Operation) http.Handler {
	byID := map[string]Operation{}
	for _, op := range reads {
		byID[op.ID] = op
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/diagnose" || r.Method != http.MethodGet ||
			r.URL.Query().Get("cursor") != "" || r.Header.Get(fleet.ForwardedHeader()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		captured := &passthrough{header: w.Header()}
		next.ServeHTTP(captured, r)
		body := captured.body.Bytes()
		if captured.status == http.StatusOK {
			if spliced, ok := withDeployment(body, func() any { return deploymentSection(r.Context(), next, byID) }); ok {
				body = spliced
				if w.Header().Get("Content-Length") != "" {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
			}
		}
		if captured.status != 0 {
			w.WriteHeader(captured.status)
		}
		_, _ = w.Write(body)
	})
}

// withDeployment is body with "deployment" added as its last key, or false
// when body is not a page the section belongs on.
func withDeployment(body []byte, section func() any) ([]byte, bool) {
	var page map[string]json.RawMessage
	if json.Unmarshal(body, &page) != nil || page == nil {
		return nil, false
	}
	if _, refused := page["error"]; refused {
		return nil, false
	}
	if _, present := page["deployment"]; present {
		return nil, false
	}
	trimmed := bytes.TrimRight(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		return nil, false
	}
	encoded, err := json.Marshal(section())
	if err != nil {
		return nil, false
	}
	out := make([]byte, 0, len(body)+len(encoded)+16)
	out = append(out, trimmed[:len(trimmed)-1]...)
	if len(page) > 0 {
		out = append(out, ',')
	}
	out = append(out, `"deployment":`...)
	out = append(out, encoded...)
	out = append(out, '}')
	return append(out, body[len(trimmed):]...), true
}

// passthrough holds the page so it can be extended; headers are the real
// writer's, set by the handler as it would have set them.
type passthrough struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *passthrough) Header() http.Header { return p.header }
func (p *passthrough) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}
func (p *passthrough) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return p.body.Write(b)
}
