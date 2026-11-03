// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package legacyoutput

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The snapshot store's failure names itself by the word the observation
// types close their list with, spelled here rather than imported.
func TestASnapshotStoreFailureNamesItsKind(t *testing.T) {
	err := &SnapshotStoreError{Err: errors.New("EOF")}
	if err.OutputFailureKind() != observability.OutputFailureSnapshotStore || observability.OutputFailureKindOf(err) != observability.OutputFailureSnapshotStore {
		t.Fatalf("kind %q, want %q", err.OutputFailureKind(), observability.OutputFailureSnapshotStore)
	}
}
