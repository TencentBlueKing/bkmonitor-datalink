// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package define

import (
	"context"
	"testing"
	"time"
)

func TestScheduledAtContext(t *testing.T) {
	want := time.Date(2026, 8, 13, 10, 20, 30, 456*int(time.Millisecond), time.UTC)
	ctx := WithScheduledAt(context.Background(), want)
	got, ok := ScheduledAtFromContext(ctx)
	if !ok || !got.Equal(want) {
		t.Fatalf("ScheduledAtFromContext() = %v, %v; want %v, true", got, ok, want)
	}
}

func TestScheduleExpiredContext(t *testing.T) {
	if ScheduleExpiredFromContext(context.Background()) {
		t.Fatal("background context must not be expired")
	}
	if !ScheduleExpiredFromContext(WithScheduleExpired(context.Background())) {
		t.Fatal("expired marker was not preserved")
	}
}
