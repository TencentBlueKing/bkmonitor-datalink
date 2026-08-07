// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestPacerNextStaysWithinMaximum(t *testing.T) {
	pacer := newPacer(3600)
	for i := 0; i < 100; i++ {
		got := pacer.Next()
		if got <= 0 || got > 3600 {
			t.Fatalf("Next() = %d, want value in (0, 3600]", got)
		}
	}
}

func TestMessageIDFitsMetadataLimit(t *testing.T) {
	client := &Client{
		instanceID: strings.Repeat("f", 16),
		maxIssued:  math.MaxUint64 - 1,
	}

	messageID := client.nextMessageID()
	if got := len(messageID); got > 64 {
		t.Fatalf("message ID length = %d, want <= 64: %s", got, messageID)
	}
	if sequence, ok := client.responseSequence(messageID); !ok || sequence != math.MaxUint64 {
		t.Fatalf("parsed sequence = (%d, %v), want (%d, true)", sequence, ok, uint64(math.MaxUint64))
	}
}

func TestInstanceIDUsesEightRandomBytes(t *testing.T) {
	instanceID, err := newInstanceID()
	if err != nil {
		t.Fatalf("newInstanceID() error: %v", err)
	}
	if got := len(instanceID); got != 16 {
		t.Fatalf("instance ID length = %d, want 16", got)
	}
}

func TestClientIgnoresOutOfOrderDataIDResponse(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})
	reloads := 0
	client := &Client{
		instanceID: "test-instance",
		storage:    storage,
		onUpdate: func(map[string]int32) {
			reloads++
		},
	}

	oldMessageID := client.nextMessageID()
	newMessageID := client.nextMessageID()
	client.handleMessage(newMessageID, []byte(`{"code":0,"data":[{"task":"basereport","dataid":3001}]}`))
	client.handleMessage(oldMessageID, []byte(`{"code":0,"data":[]}`))

	if got, ok := storage.GetTaskDataID("basereport"); !ok || got != 3001 {
		t.Fatalf("basereport data ID after out-of-order response = (%d, %v), want (3001, true)", got, ok)
	}
	if reloads != 1 {
		t.Fatalf("reload count = %d, want 1", reloads)
	}
}

func TestClientIgnoresResponseFromPreviousProcess(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})
	client := &Client{
		instanceID: "current-instance",
		storage:    storage,
		onUpdate:   func(map[string]int32) {},
	}
	client.nextMessageID()

	client.handleMessage(
		fmt.Sprintf("bkmonitorbeat.%s.previous-instance.1", TypeFetchHostDataID),
		[]byte(`{"code":0,"data":[{"task":"basereport","dataid":2001}]}`),
	)

	if _, ok := storage.GetTaskDataID("basereport"); ok {
		t.Fatal("response from previous process must not update storage")
	}
}

func TestClientAllowsSameResponseToRetryPendingReload(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})
	reloads := 0
	client := &Client{
		instanceID: "test-instance",
		storage:    storage,
		onUpdate: func(map[string]int32) {
			reloads++
		},
	}
	messageID := client.nextMessageID()
	content := []byte(`{"code":0,"data":[{"task":"basereport","dataid":2001}]}`)

	client.handleMessage(messageID, content)
	client.handleMessage(messageID, content)

	if reloads != 2 {
		t.Fatalf("reload count = %d, want 2 while revision is pending", reloads)
	}
}

func TestClientShortensPollWhenExpectedDataIDIsMissing(t *testing.T) {
	storage := NewStorage()
	storage.SetExpectedTasks([]string{"basereport"})
	pacer := newPacer(3600)
	for i := 0; i < 10; i++ {
		pacer.Next()
	}
	client := &Client{
		instanceID: "test-instance",
		storage:    storage,
		pacer:      pacer,
		retrySoon:  make(chan struct{}, 1),
		onUpdate:   func(map[string]int32) {},
	}
	messageID := client.nextMessageID()

	client.handleMessage(messageID, []byte(`{"code":0,"data":[]}`))

	if got := pacer.Next(); got < 120 || got > 240 {
		t.Fatalf("poll delay after missing response = %d, want [120, 240]", got)
	}
	select {
	case <-client.retrySoon:
	default:
		t.Fatal("missing response must wake the polling loop")
	}
}
