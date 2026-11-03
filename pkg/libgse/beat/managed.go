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
	"fmt"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/outputs/console"
)

// managedPreviewState implements the opt-in lifecycle used by bkunifylogbeat's
// -T Kafka preview. It is disabled by default: other libgse callers keep the
// original Send/SendEvent behavior and the original Stop resource ordering.
type managedPreviewState struct {
	mu       sync.Mutex
	enabled  bool
	inFlight int64
	firstErr error
	changed  chan struct{}
}

func newManagedPreviewState() *managedPreviewState {
	return &managedPreviewState{changed: make(chan struct{}, 1)}
}

var managedPreview = newManagedPreviewState()

func (s *managedPreviewState) signalLocked() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *managedPreviewState) isEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// beginPublish records events handed to the publisher pipeline. It must be
// called immediately before the underlying client Publish/PublishAll.
func (s *managedPreviewState) beginPublish(count int) {
	if count <= 0 {
		return
	}
	s.mu.Lock()
	if s.enabled {
		s.inFlight += int64(count)
	}
	s.mu.Unlock()
}

// recordError stores the first terminal output error and wakes any waiter.
// Console reports it from its output worker, so this must never block.
func (s *managedPreviewState) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.firstErr == nil {
		s.firstErr = err
		s.signalLocked()
	}
	s.mu.Unlock()
}

// EnableManagedPreview enables the finite preview lifecycle. It is only valid
// in -T mode. The console error observer is registered before the caller starts
// tasks, so the first event publish is already covered.
func EnableManagedPreview() error {
	if testMode == nil || !*testMode {
		return errors.New("managed preview requires -T mode")
	}

	s := managedPreview
	s.mu.Lock()
	if s.enabled {
		s.mu.Unlock()
		return nil
	}
	s.enabled = true
	s.inFlight = 0
	s.firstErr = nil
	s.changed = make(chan struct{}, 1)
	s.mu.Unlock()

	// The publisher client is created during InitWithPublishConfig, before the
	// preview coordinator runs. Wrap it now so every publish path (including
	// libgse helpers that call the client directly) participates in accounting.
	commonBKBeat.wrapManagedClient()
	console.SetErrorHandler(s.recordError)
	return nil
}

// DisableManagedPreview restores the default lifecycle. The wrapped client, if
// any, stays in place but stops counting because enabled is false.
func DisableManagedPreview() {
	s := managedPreview
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}
	s.enabled = false
	s.inFlight = 0
	s.firstErr = nil
	s.mu.Unlock()

	console.SetErrorHandler(nil)
}

// PublishAck retires a batch of events reported by the libbeat ACK handler.
// A negative counter indicates duplicate ACK delivery or an unbalanced publish
// path; it is recorded as a terminal preview error instead of silently
// reporting success.
func PublishAck(count int) {
	if count <= 0 {
		return
	}

	s := managedPreview
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return
	}
	s.inFlight -= int64(count)
	if s.inFlight < 0 {
		s.inFlight = 0
		if s.firstErr == nil {
			s.firstErr = fmt.Errorf(
				"publish ack underflow after acking %d events: ACK callback may be registered twice", count)
		}
	}
	s.signalLocked()
}

// WaitPublishIdle waits until every published event has been acknowledged and
// all console output batches have been flushed, or until timeout. It returns
// the first console error immediately.
func WaitPublishIdle(timeout time.Duration) error {
	s := managedPreview

	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return errors.New("managed preview is not enabled")
	}
	deadline := time.Now().Add(timeout)
	for {
		if s.firstErr != nil {
			err := s.firstErr
			s.mu.Unlock()
			return err
		}
		if s.inFlight == 0 {
			s.mu.Unlock()
			return nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			inFlight := s.inFlight
			err := s.firstErr
			s.mu.Unlock()
			if err != nil {
				return err
			}
			return fmt.Errorf("publish drain timed out with %d events in flight", inFlight)
		}

		changed := s.changed
		s.mu.Unlock()

		timer := time.NewTimer(remaining)
		select {
		case <-changed:
		case <-timer.C:
		}
		if !timer.Stop() {
			// The timer may have fired simultaneously with the change signal;
			// drain a pending tick without blocking.
			select {
			case <-timer.C:
			default:
			}
		}

		s.mu.Lock()
	}
}

// managedClient counts every event handed to the publisher pipeline. Embedding
// beat.Client keeps Close and any future methods forwarding unchanged.
type managedClient struct {
	beat.Client
}

func (c *managedClient) Publish(event beat.Event) {
	managedPreview.beginPublish(1)
	c.Client.Publish(event)
}

func (c *managedClient) PublishAll(events []beat.Event) {
	managedPreview.beginPublish(len(events))
	c.Client.PublishAll(events)
}

// wrapManagedClient is idempotent. It is called only from EnableManagedPreview,
// before the preview tasks start publishing.
func (bkb *BKBeat) wrapManagedClient() {
	bkb.clientMu.Lock()
	defer bkb.clientMu.Unlock()

	if bkb.Client == nil {
		return
	}
	current := *bkb.Client
	if _, ok := current.(*managedClient); ok {
		return
	}
	wrapped := beat.Client(&managedClient{Client: current})
	bkb.Client = &wrapped
}
