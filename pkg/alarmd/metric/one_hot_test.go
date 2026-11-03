// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"regexp"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// oneHotHelp is how a family's HELP says it is one-hot.
var oneHotHelp = regexp.MustCompile(`(?i)one-hot|1 on the current`)

var descriptorHelp = regexp.MustCompile(`fqName: "([^"]+)", help: "((?:[^"\\]|\\.)*)"`)

// A gauge that says it is one-hot is in OneHotFamilies, where the role guard
// reads it on every role shape; and every listed family is registered.
func TestEveryOneHotGaugeIsListed(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	descriptors := make(chan *prometheus.Desc)
	go func() {
		recorder.registry.Describe(descriptors)
		close(descriptors)
	}()
	registered := map[string]bool{}
	for descriptor := range descriptors {
		match := descriptorHelp.FindStringSubmatch(descriptor.String())
		if match == nil {
			continue
		}
		name, help := match[1], match[2]
		registered[name] = true
		if oneHotHelp.MatchString(help) && !slices.Contains(OneHotFamilies, name) {
			t.Errorf("%s says it is one-hot but is not in OneHotFamilies, so no role guard reads it", name)
		}
	}
	for _, name := range OneHotFamilies {
		if !registered[name] {
			t.Errorf("OneHotFamilies lists %s, which is not registered", name)
		}
	}
}

// The span export's role state is the word every one-hot family uses for a
// role it does not describe, and the gauge creates it with the others.
func TestTheSpanExportsRoleStateIsTheSharedWord(t *testing.T) {
	if fleet.TracingOffRole != RoleNotApplicable || !slices.Contains(fleet.TracingStates, RoleNotApplicable) {
		t.Fatalf("fleet.TracingOffRole %q, states %v; want %q among them", fleet.TracingOffRole, fleet.TracingStates, RoleNotApplicable)
	}
}
