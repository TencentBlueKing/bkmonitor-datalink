// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store_test

import (
	"reflect"
	"testing"

	"linkd/internal/store"
)

func TestLogicalAlertContractDoesNotExposePhysicalArchiveState(t *testing.T) {
	t.Parallel()
	storedType := reflect.TypeOf(store.StoredAlert{})
	for _, name := range []string{
		"Location",
		"ContentDigest",
		"ArchiveState",
		"ArchivePendingSince",
		"ArchivedTime",
	} {
		if _, exists := storedType.FieldByName(name); exists {
			t.Errorf("StoredAlert unexpectedly exposes physical field %s", name)
		}
	}

	repositoryType := reflect.TypeOf((*store.Repository)(nil)).Elem()
	for _, name := range []string{
		"ScanPendingAlertArchives",
		"CreateAlertHistory",
		"GetAlertHistory",
		"CompleteAlertArchive",
	} {
		if _, exists := repositoryType.MethodByName(name); exists {
			t.Errorf("Repository unexpectedly exposes physical method %s", name)
		}
	}
}
