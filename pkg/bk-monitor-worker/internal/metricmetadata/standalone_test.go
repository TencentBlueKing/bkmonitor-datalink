// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package metricmetadata

import (
	"context"
	"errors"
	"testing"
)

func TestRunStandalone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunStandalone(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunStandalone() error = %v, want context.Canceled", err)
	}
}
