// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"testing"
)

func TestNumZombieProcsUsesSnapshot(t *testing.T) {
	originalGetSharedProcStates := getSharedProcStates
	defer func() {
		getSharedProcStates = originalGetSharedProcStates
	}()

	getSharedProcStates = func() (map[int32]string, bool) {
		return map[int32]string{1: "zombie", 2: "running", 3: "zombie"}, true
	}

	total, err := numZombieProcs()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if total != 2 {
		t.Fatalf("expected zombie count 2, got %d", total)
	}
}

func TestNumZombieProcsFallsBackToGopsutil(t *testing.T) {
	originalGetSharedProcStates := getSharedProcStates
	originalCountZombieProcs := countZombieProcs
	defer func() {
		getSharedProcStates = originalGetSharedProcStates
		countZombieProcs = originalCountZombieProcs
	}()

	getSharedProcStates = func() (map[int32]string, bool) {
		return nil, false
	}
	countZombieProcs = func() (int, error) {
		return 2, nil
	}

	total, err := numZombieProcs()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if total != 2 {
		t.Fatalf("expected zombie count 2, got %d", total)
	}
}
