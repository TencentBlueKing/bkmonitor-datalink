// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventsource

import (
	"encoding/json"
	"strings"
	"testing"

	"linkd/internal/config"
)

func TestSourcePublicationDoesNotContainKACDeploymentConfiguration(t *testing.T) {
	service := New(newDocs(), config.SeverityConfig{})
	spec := sample()
	record, err := service.Apply(t.Context(), spec, 0, false, "test")
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.GetRelease(t.Context(), spec.EventSourceID, record.Published)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kac_targets", "projection_endpoint", "internal_token", "plugins"} {
		if strings.Contains(string(raw), key) {
			t.Fatal("deployment config entered release", key)
		}
	}
}
