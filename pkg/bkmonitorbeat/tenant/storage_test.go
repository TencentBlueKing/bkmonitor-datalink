// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import "testing"

func TestStorageUpdateTaskDataIDsReplacesSnapshot(t *testing.T) {
	storage := NewStorage()
	storage.UpdateTaskDataIDs(map[string]int32{
		"basereport":    1001,
		"exceptionbeat": 1000,
	})

	if updated := storage.UpdateTaskDataIDs(map[string]int32{"basereport": 2001}); !updated {
		t.Fatal("expected changed data ID to update storage")
	}

	if got, ok := storage.GetTaskDataID("basereport"); !ok || got != 2001 {
		t.Fatalf("basereport data ID = (%d, %v), want (2001, true)", got, ok)
	}
	if got, ok := storage.GetTaskDataID("exceptionbeat"); ok {
		t.Fatalf("exceptionbeat data ID = (%d, %v), want missing after authoritative snapshot replacement", got, ok)
	}
}

func TestStorageResolveTaskDataID(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})

	if got := storage.ResolveTaskDataID("basereport", 1001); got != 0 {
		t.Fatalf("missing expected task resolved to %d, want 0", got)
	}
	if got := storage.ResolveTaskDataID("exceptionbeat", 1000); got != 1000 {
		t.Fatalf("unexpected task resolved to %d, want static data ID 1000", got)
	}

	storage.UpdateTaskDataIDs(map[string]int32{
		"basereport": 2001,
		"unexpected": 9999,
	})
	if got := storage.ResolveTaskDataID("basereport", 1001); got != 2001 {
		t.Fatalf("mapped task resolved to %d, want dynamic data ID 2001", got)
	}
	if _, ok := storage.GetTaskDataID("unexpected"); ok {
		t.Fatal("unexpected task should not be stored")
	}
}

func TestStorageRetriesPendingUpdateUntilApplied(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})
	tasks := map[string]int32{"basereport": 2001}

	if updated := storage.UpdateTaskDataIDs(tasks); !updated {
		t.Fatal("expected first data ID response to trigger reload")
	}
	if updated := storage.UpdateTaskDataIDs(tasks); !updated {
		t.Fatal("expected unapplied data ID response to retry reload")
	}

	storage.MarkApplied(storage.Revision())
	if updated := storage.UpdateTaskDataIDs(tasks); updated {
		t.Fatal("expected applied data ID response not to trigger reload")
	}
}

func TestStorageCandidateResolverDoesNotAffectCommittedTasks(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport", "exceptionbeat"})
	storage.UpdateTaskDataIDs(map[string]int32{
		"basereport":    2001,
		"exceptionbeat": 2000,
	})
	storage.MarkApplied(storage.Revision())
	candidate := storage.NewResolver([]string{"basereport", "gather_up_beat"})

	if got := candidate.ResolveTaskDataID("exceptionbeat", 1000); got != 1000 {
		t.Fatalf("candidate exceptionbeat data ID = %d, want static data ID 1000", got)
	}
	if got := candidate.ResolveTaskDataID("basereport", 1001); got != 2001 {
		t.Fatalf("candidate basereport data ID = %d, want 2001", got)
	}
	if got := candidate.ResolveTaskDataID("gather_up_beat", 1100017); got != 0 {
		t.Fatalf("candidate gather-up data ID = %d, want 0 before mapping is available", got)
	}
	if got := storage.ResolveTaskDataID("exceptionbeat", 1000); got != 2000 {
		t.Fatalf("committed exceptionbeat data ID = %d, want 2000", got)
	}

	if updated := storage.UpdateTaskDataIDs(map[string]int32{
		"basereport":    2001,
		"exceptionbeat": 3000,
	}); !updated {
		t.Fatal("expected committed exceptionbeat update to be accepted")
	}
	if got := storage.ResolveTaskDataID("exceptionbeat", 1000); got != 3000 {
		t.Fatalf("committed exceptionbeat data ID after update = %d, want 3000", got)
	}
	if got := candidate.ResolveTaskDataID("exceptionbeat", 1000); got != 1000 {
		t.Fatalf("candidate exceptionbeat data ID after update = %d, want static data ID 1000", got)
	}
}
