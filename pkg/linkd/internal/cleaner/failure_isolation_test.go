// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/processingerror"
)

type mixedProcessor struct {
	mu    sync.Mutex
	calls map[string]int
}

func (p *mixedProcessor) Process(_ context.Context, m consume.Message) (ProcessResult, error) {
	p.mu.Lock()
	p.calls[m.ID]++
	p.mu.Unlock()
	switch m.ID {
	case "logic":
		return ProcessResult{}, errors.New("invalid JSON")
	case "panic":
		panic("private payload")
	case "502":
		return ProcessResult{}, processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}
	}
	return runtimeTestProcessor{}.Process(context.Background(), m)
}

func TestBadMessagesAndPanicDoNotStopCleaner(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := newRuntimeTestSession(cancel, struct{ lane, id string }{"lane", "logic"}, struct{ lane, id string }{"lane", "panic"}, struct{ lane, id string }{"lane", "502"}, struct{ lane, id string }{"lane", "good"})
	session.cancelAfter = 4
	p := &mixedProcessor{calls: map[string]int{}}
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	cfg := config.DefaultCleanerRuntimeConfig()
	cfg.RetryMaxAttempts = 20
	r, err := NewRuntime(cfg, session, p, &runtimeTestWriter{}, mailbox, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls["logic"] != 1 || p.calls["panic"] != 1 || p.calls["502"] != 3 || p.calls["good"] != 1 {
		t.Fatal(p.calls)
	}
	if len(session.confirmed) != 4 || len(mailbox.byLane["all"]) != 1 || mailbox.byLane["all"][0] != "good" {
		t.Fatal(session.confirmed, mailbox.byLane)
	}
}
