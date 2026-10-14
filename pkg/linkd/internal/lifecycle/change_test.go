// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type changePublisherFunc func(context.Context, domain.AlertChange) error

func (f changePublisherFunc) Publish(ctx context.Context, c domain.AlertChange) error {
	return f(ctx, c)
}

func TestLifecyclePublishesSmallActionsWithoutBlockingOnQueueFailure(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, NoopFinalHook{})
	changes := []domain.AlertChange{}
	processor.publisher = changePublisherFunc(func(_ context.Context, c domain.AlertChange) error {
		changes = append(changes, c)
		return errors.New("redis unavailable")
	})
	event := storetest.Event("tenant", "event", "fp", "warning")
	created := persistAndProcess(t, repo, processor, event)
	if len(changes) != 1 || changes[0].Action == nil || changes[0].Action.Type != "firing" {
		t.Fatal("admitted firing missing", changes)
	}
	command := CloseAlertCommand{OperationID: "close", BKTenantID: event.BKTenantID, AlertID: created.AlertIDs[0], OperatorID: "operator", OperatorKind: domain.OperatorKindUser, Reason: "done", EffectiveAt: event.ReceivedAt.Add(time.Hour)}
	if _, err := processor.CloseAlert(t.Context(), command); err != nil {
		t.Fatal("queue failure blocked close", err)
	}
	if _, err := processor.CloseAlert(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[1].Action.Type != "close" || changes[1].Action.ID != changes[2].Action.ID {
		t.Fatal("close identity changed", changes)
	}
	raw, _ := json.Marshal(changes[1])
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if len(raw) > 1024 || body["alert"] != nil || body["projection"] != nil || body["content_hash"] != nil {
		t.Fatal("notification contains snapshot", string(raw))
	}
}
