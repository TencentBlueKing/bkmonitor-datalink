// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type indexMaintainerFunc func(context.Context) error

func (f indexMaintainerFunc) Maintain(ctx context.Context) error { return f(ctx) }

func TestKACIndexFailureRemainsObservedWithoutStoppingRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := taskCatalog(deliveryConfigFixture(), 0)
	done := make(chan error, 1)
	go func() {
		done <- runKACIndexMaintenance(ctx, indexMaintainerFunc(func(context.Context) error { return errors.New("fixture unavailable") }), registry, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	deadline := time.After(time.Second)
	for deliveryTask(t, registry, "kac-index-maintenance").Execution.Failed == 0 {
		select {
		case err := <-done:
			t.Fatal("dependency failure stopped runtime", err)
		case <-deadline:
			t.Fatal("dependency failure was not observed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-done:
		t.Fatal("runtime exited after failure", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("maintenance did not exit on cancellation")
	}
	if deliveryTask(t, registry, "kac-index-maintenance").Active {
		t.Fatal("cancelled maintenance stayed active")
	}
}
