// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"strings"
	"testing"
)

// Every field the package reads must have a home in the publisher's tree. A
// field added to Fields without a DBKey case would subscribe to
// "<prefix>:<tenant>:" and read nothing, without any error saying so; the
// closed switch has to be closed over the same list the reader iterates.
func TestEveryFieldHasADomainKeyThatEndsWithItsName(t *testing.T) {
	seen := map[string]Field{}
	for _, field := range Fields {
		key := field.DBKey()
		if key == "" {
			t.Fatalf("field %q has no DB key: it would be subscribed under an empty key and never read", field)
		}
		if !strings.HasPrefix(key, "base_config.") || !strings.HasSuffix(key, "."+string(field)) {
			t.Fatalf("field %q DB key %q is not base_config.<domain>.<field>", field, key)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("fields %q and %q share DB key %q", other, field, key)
		}
		seen[key] = field
	}
	if got := Field("not_a_field").DBKey(); got != "" {
		t.Fatalf("an unknown field must have no key, got %q", got)
	}
}
