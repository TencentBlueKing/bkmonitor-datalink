// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/beat"
)

type fakeManagedClient struct {
	mu        sync.Mutex
	published int
	closed    int
	events    []beat.Event
	onClose   func()
}

func (c *fakeManagedClient) Publish(event beat.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.published++
	c.events = append(c.events, event)
}

func (c *fakeManagedClient) PublishAll(events []beat.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.published += len(events)
	c.events = append(c.events, events...)
}

func (c *fakeManagedClient) Close() error {
	c.mu.Lock()
	c.closed++
	hook := c.onClose
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (c *fakeManagedClient) publishedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.published
}

func (c *fakeManagedClient) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func resetManagedPreview(t *testing.T) {
	t.Helper()
	old := managedPreview
	managedPreview = newManagedPreviewState()
	t.Cleanup(func() { managedPreview = old })
}

func enableTestMode(t *testing.T) {
	t.Helper()
	old := *testMode
	*testMode = true
	t.Cleanup(func() { *testMode = old })
}

func installFakeClient(t *testing.T) *fakeManagedClient {
	t.Helper()
	fake := &fakeManagedClient{}
	var client beat.Client = fake
	commonBKBeat.setClient(&client)
	return fake
}

func TestManagedPreviewPublishAccounting(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	fake := installFakeClient(t)

	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	client := commonBKBeat.getClient()
	if _, ok := (*client).(*managedClient); !ok {
		t.Fatal("publisher client was not wrapped for managed accounting")
	}

	if !SendEvent(Event{Fields: MapStr{"field": "value"}}) {
		t.Fatal("SendEvent returned false")
	}
	if got := fake.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}

	if err := WaitPublishIdle(20 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("WaitPublishIdle() error = %v, want timeout", err)
	}

	PublishAck(1)
	if err := WaitPublishIdle(200 * time.Millisecond); err != nil {
		t.Fatalf("WaitPublishIdle() after ack error = %v", err)
	}
}

func TestManagedPreviewPublishAllAccounting(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	fake := installFakeClient(t)
	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	client := commonBKBeat.getClient()
	(*client).PublishAll([]beat.Event{
		{Fields: MapStr{"a": "1"}},
		{Fields: MapStr{"a": "2"}},
		{Fields: MapStr{"a": "3"}},
	})
	if got := fake.publishedCount(); got != 3 {
		t.Fatalf("published = %d, want 3", got)
	}
	PublishAck(3)
	if err := WaitPublishIdle(200 * time.Millisecond); err != nil {
		t.Fatalf("WaitPublishIdle() error = %v", err)
	}
}

func TestManagedPreviewAckUnderflow(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	installFakeClient(t)
	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	PublishAck(1)
	err := WaitPublishIdle(100 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "underflow") {
		t.Fatalf("WaitPublishIdle() error = %v, want underflow", err)
	}
}

func TestManagedPreviewFirstConsoleError(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	installFakeClient(t)
	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	expected := errors.New("console flush failed")
	managedPreview.recordError(expected)
	managedPreview.recordError(errors.New("later error"))

	err := WaitPublishIdle(time.Second)
	if !errors.Is(err, expected) {
		t.Fatalf("WaitPublishIdle() error = %v, want first error %v", err, expected)
	}
}

func TestManagedPreviewStopDefersClientAndStorage(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	fake := installFakeClient(t)

	originalCloseStorage := closeStorage
	t.Cleanup(func() { closeStorage = originalCloseStorage })
	closeCount := 0
	closeStorage = func() { closeCount++ }
	markStorageInited()

	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	commonBKBeat.Stop()
	if !isDoneClosed(commonBKBeat.Done) {
		t.Fatal("Done channel was not closed")
	}
	if got := fake.closeCount(); got != 0 {
		t.Fatalf("managed Stop closed publisher client %d times, want 0", got)
	}
	if closeCount != 0 {
		t.Fatalf("managed Stop released storage %d times, want 0", closeCount)
	}

	// Simulate the package-level cleanup that runs after Registrar.Stop.
	commonBKBeat.stopPublish()
	freeResource()
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("publisher client Close calls = %d, want 1", got)
	}
	if closeCount != 1 {
		t.Fatalf("storage Close calls = %d, want 1", closeCount)
	}
}

func TestPackageStopClosesClientBeforeStorage(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	fake := installFakeClient(t)

	var order []string
	fake.onClose = func() { order = append(order, "client") }

	originalCloseStorage := closeStorage
	t.Cleanup(func() { closeStorage = originalCloseStorage })
	closeStorage = func() { order = append(order, "storage") }
	markStorageInited()

	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}
	defer DisableManagedPreview()

	// Framework signal path: beater Stop runs first and must not release the
	// client or storage.
	commonBKBeat.Stop()
	if len(order) != 0 {
		t.Fatalf("managed BKBeat.Stop released resources: %v", order)
	}

	// Package cleanup after Registrar.Stop: client must be closed before the
	// Storage is released.
	if err := Stop(); err != nil {
		t.Fatalf("package Stop() error = %v", err)
	}
	if len(order) != 2 || order[0] != "client" || order[1] != "storage" {
		t.Fatalf("cleanup order = %v, want [client storage]", order)
	}
}

func TestDisableManagedPreviewStopsAccounting(t *testing.T) {
	resetBeatState(t)
	resetManagedPreview(t)
	enableTestMode(t)

	mutex.Lock()
	commonBKBeat.BeaterState = BeaterRunning
	mutex.Unlock()
	fake := installFakeClient(t)
	if err := EnableManagedPreview(); err != nil {
		t.Fatalf("EnableManagedPreview() error = %v", err)
	}

	DisableManagedPreview()
	client := commonBKBeat.getClient()
	if client == nil {
		t.Fatal("publisher client was cleared by DisableManagedPreview")
	}
	(*client).Publish(Event{Fields: MapStr{"field": "value"}})
	PublishAck(1) // must be ignored instead of causing an underflow
	if err := WaitPublishIdle(time.Second); err == nil {
		t.Fatal("WaitPublishIdle should fail when managed preview is disabled")
	}
	if got := fake.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
}

type fakePipeline struct {
	mu           sync.Mutex
	ackHandler   beat.PipelineACKHandler
	clientConfig beat.ClientConfig
	setACKCalls  int
	connectCalls int
	client       beat.Client
	connected    chan struct{}
}

func newFakePipeline(client beat.Client) *fakePipeline {
	return &fakePipeline{
		client:    client,
		connected: make(chan struct{}),
	}
}

func (p *fakePipeline) SetACKHandler(handler beat.PipelineACKHandler) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ackHandler = handler
	p.setACKCalls++
	return nil
}

func (p *fakePipeline) ConnectWith(cfg beat.ClientConfig) (beat.Client, error) {
	p.mu.Lock()
	p.clientConfig = cfg
	p.connectCalls++
	p.mu.Unlock()
	close(p.connected)
	return p.client, nil
}

func (p *fakePipeline) Connect() (beat.Client, error) {
	return p.ConnectWith(beat.ClientConfig{})
}

func TestBKBeatRunRegistersSingleACKHandler(t *testing.T) {
	resetBeatState(t)

	oldPublishConfig := publishConfig
	oldErrorChan := errorMessageChan
	t.Cleanup(func() {
		publishConfig = oldPublishConfig
		errorMessageChan = oldErrorChan
	})

	publishConfig = PublishConfig{ACKEvents: func([]interface{}) {}}
	errorMessageChan = make(chan error)
	fake := &fakeManagedClient{}
	pipeline := newFakePipeline(fake)
	commonBKBeat.Beat = &beat.Beat{Publisher: pipeline}

	wg.Add(1)
	runDone := make(chan error, 1)
	go func() { runDone <- commonBKBeat.Run(commonBKBeat.Beat) }()

	select {
	case <-pipeline.connected:
	case <-time.After(time.Second):
		t.Fatal("BKBeat.Run did not connect")
	}
	if !isDoneClosed(commonBKBeat.Done) {
		close(commonBKBeat.Done)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("BKBeat.Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BKBeat.Run did not return after Done was closed")
	}

	pipeline.mu.Lock()
	defer pipeline.mu.Unlock()
	if pipeline.setACKCalls != 1 {
		t.Fatalf("SetACKHandler calls = %d, want 1", pipeline.setACKCalls)
	}
	if pipeline.connectCalls != 1 {
		t.Fatalf("ConnectWith calls = %d, want 1", pipeline.connectCalls)
	}
	if pipeline.clientConfig.ACKEvents != nil {
		t.Fatal("client config still contains ACKEvents; the same callback would run twice")
	}
	if pipeline.ackHandler.ACKEvents == nil {
		t.Fatal("pipeline-level ACK handler was not registered")
	}
}
