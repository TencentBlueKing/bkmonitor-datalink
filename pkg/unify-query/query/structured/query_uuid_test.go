// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"testing"
	"time"
)

func TestQueryTs_GenerateQueryUUID(t *testing.T) {
	queryTs := &QueryTs{
		SpaceUid: "test_space",
		QueryList: []*Query{
			{
				TableID:       "system.cpu_summary",
				FieldName:     "usage",
				ReferenceName: "a",
			},
		},
		MetricMerge: "a",
		Start:       "1657848000",
		End:         "1657851600",
		Step:        "1m",
		Scroll:      "5m",
	}

	timeout := 5 * time.Minute
	queryUUID, err := queryTs.GenerateQueryUUID(timeout)
	if err != nil {
		t.Fatalf("GenerateQueryUUID failed: %v", err)
	}

	if queryUUID == "" {
		t.Fatal("Generated queryUUID is empty")
	}

	t.Logf("Generated queryUUID: %s", queryUUID)

	info, err := ParseQueryUUID(queryUUID)
	if err != nil {
		t.Fatalf("ParseQueryUUID failed: %v", err)
	}

	expectedExpiry := time.Now().Add(timeout)
	timeDiff := info.ExpiresAt.Sub(expectedExpiry)
	if timeDiff > time.Second || timeDiff < -time.Second {
		t.Errorf("Expiry time mismatch. Expected around %v, got %v", expectedExpiry, info.ExpiresAt)
	}

	err = queryTs.VerifyQueryUUID(queryUUID)
	if err != nil {
		t.Fatalf("VerifyQueryUUID failed: %v", err)
	}

	t.Log("QueryUUID verification passed")
}

func TestQueryUUID_Expiration(t *testing.T) {
	queryTs := &QueryTs{
		SpaceUid: "test_space",
		QueryList: []*Query{
			{
				TableID:       "system.cpu_summary",
				FieldName:     "usage",
				ReferenceName: "a",
			},
		},
		MetricMerge: "a",
	}

	longTimeout := 5 * time.Minute
	queryUUID, err := queryTs.GenerateQueryUUID(longTimeout)
	if err != nil {
		t.Fatalf("GenerateQueryUUID failed: %v", err)
	}

	err = ValidateQueryUUID(queryUUID)
	if err != nil {
		t.Fatalf("Immediate validation should succeed: %v", err)
	}

	shortTimeout := 1 * time.Second
	shortUUID, err := queryTs.GenerateQueryUUID(shortTimeout)
	if err != nil {
		t.Fatalf("GenerateQueryUUID with short timeout failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	err = ValidateQueryUUID(shortUUID)
	if err == nil {
		t.Fatal("Expected queryUUID to be expired, but validation succeeded")
	}

	if !IsQueryUUIDExpired(shortUUID) {
		t.Fatal("IsQueryUUIDExpired should return true for expired UUID")
	}

	t.Log("Expiration test passed")
}

func TestQueryUUID_DifferentQueries(t *testing.T) {
	queryTs1 := &QueryTs{
		SpaceUid: "test_space",
		QueryList: []*Query{
			{
				TableID:       "system.cpu_summary",
				FieldName:     "usage",
				ReferenceName: "a",
			},
		},
		MetricMerge: "a",
	}

	queryTs2 := &QueryTs{
		SpaceUid: "test_space",
		QueryList: []*Query{
			{
				TableID:       "system.memory_summary",
				FieldName:     "usage",
				ReferenceName: "a",
			},
		},
		MetricMerge: "a",
	}

	timeout := 5 * time.Minute

	uuid1, err := queryTs1.GenerateQueryUUID(timeout)
	if err != nil {
		t.Fatalf("GenerateQueryUUID for queryTs1 failed: %v", err)
	}

	uuid2, err := queryTs2.GenerateQueryUUID(timeout)
	if err != nil {
		t.Fatalf("GenerateQueryUUID for queryTs2 failed: %v", err)
	}

	if uuid1 == uuid2 {
		t.Fatal("Different queries should generate different UUIDs")
	}

	err = queryTs2.VerifyQueryUUID(uuid1)
	if err == nil {
		t.Fatal("queryTs2 should not match queryTs1's UUID")
	}

	err = queryTs1.VerifyQueryUUID(uuid2)
	if err == nil {
		t.Fatal("queryTs1 should not match queryTs2's UUID")
	}

	t.Log("Different queries test passed")
}

func TestQueryUUID_SameQueryMultiplePods(t *testing.T) {
	queryTs := &QueryTs{
		SpaceUid: "test_space",
		QueryList: []*Query{
			{
				TableID:       "system.cpu_summary",
				FieldName:     "usage",
				ReferenceName: "a",
			},
		},
		MetricMerge: "a",
		Start:       "1657848000",
		End:         "1657851600",
	}

	timeout := 5 * time.Minute

	uuid1, err := queryTs.GenerateQueryUUID(timeout)
	if err != nil {
		t.Fatalf("Pod1 GenerateQueryUUID failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	uuid2, err := queryTs.GenerateQueryUUID(timeout)
	if err != nil {
		t.Fatalf("Pod2 GenerateQueryUUID failed: %v", err)
	}

	if uuid1 == uuid2 {
		t.Log("UUIDs are the same (this could happen if generated at exactly the same second)")
	} else {
		t.Log("UUIDs are different due to different timestamps (expected)")
	}

	err = queryTs.VerifyQueryUUID(uuid1)
	if err != nil {
		t.Fatalf("queryTs should verify uuid1: %v", err)
	}

	err = queryTs.VerifyQueryUUID(uuid2)
	if err != nil {
		t.Fatalf("queryTs should verify uuid2: %v", err)
	}

	t.Log("Multiple pods test passed")
}

func TestParseQueryUUID_InvalidInput(t *testing.T) {
	_, err := ParseQueryUUID("invalid-base64!")
	if err == nil {
		t.Fatal("Should fail with invalid base64")
	}

	shortData := "dGVzdA==" // "test" in base64, too short
	_, err = ParseQueryUUID(shortData)
	if err == nil {
		t.Fatal("Should fail with wrong length")
	}

	t.Log("Invalid input test passed")
}
