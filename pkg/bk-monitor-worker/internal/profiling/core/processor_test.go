// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestHandleMessageContinuesAfterInvalidEvent(t *testing.T) {
	store := &recordingStore{}
	processor, err := NewProcessor(store, Options{})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(`[
		{"event_name":"active_profiling_started","timestamp":100,"dimension":{"bk_tenant_id":"system","bk_biz_id":2,"bk_host_id":101,"scheduled_at":100,"schedule_type":"manual"},"event":{"content":{"started_at":100}}},
		{"event_name":"active_profiling_finished","timestamp":200,"dimension":{"bk_tenant_id":"system","bk_biz_id":2,"bk_host_id":102,"scheduled_at":100,"schedule_type":"manual","status":"succeed"},"event":{"content":{"started_at":100,"finished_at":200,"execution_duration_ms":-1}}},
		{"event_name":"active_profiling_started","timestamp":300,"dimension":{"bk_tenant_id":"system","bk_biz_id":2,"bk_host_id":103,"scheduled_at":100,"schedule_type":"manual"},"event":{"content":{"started_at":300}}}
	]`)

	err = processor.HandleMessage(context.Background(), payload)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("HandleMessage() error = %v, want ErrInvalidEvent", err)
	}
	if want := []int{101, 103}; !reflect.DeepEqual(store.appliedHostIDs, want) {
		t.Fatalf("applied host IDs = %v, want %v", store.appliedHostIDs, want)
	}
}

type recordingStore struct {
	appliedHostIDs []int
}

func (s *recordingStore) ApplyEvent(_ context.Context, event Event, _ int64, _ WindowPolicy) error {
	s.appliedHostIDs = append(s.appliedHostIDs, event.HostID)
	return nil
}

func (*recordingStore) MarkUnknown(context.Context, int64, int) (int, error) {
	return 0, nil
}
