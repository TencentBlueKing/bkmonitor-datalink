// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obevidence

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/sourcereads"
)

// Every key alarmd reads from a strategy document is shown as written by the
// source view: a refused strategy is read and replayed from that view, and a
// key alarmd reads that the view shows only by its shape is a replay that
// cannot reproduce the refusal. A key alarmd starts reading without the view
// following fails here.
func TestTheSourceViewShowsEveryKeyAlarmdReads(t *testing.T) {
	var missing []string
	for _, path := range sourcereads.Paths() {
		p := sourcePolicy
		for _, segment := range strings.Split(path, ".") {
			child := p.fields[segment]
			if child == nil {
				child = p.values
			}
			if child == nil {
				missing = append(missing, path)
				break
			}
			p = child
		}
	}
	if len(missing) > 0 {
		t.Fatalf("alarmd reads %d keys the source view shows only by shape:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
