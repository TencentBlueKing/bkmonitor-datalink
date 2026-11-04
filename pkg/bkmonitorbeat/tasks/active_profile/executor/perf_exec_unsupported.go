// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !linux

package executor

import (
	"context"
	"fmt"
)

func executePerf(_ context.Context, _ perfConfig) (perfResult, error) {
	err := MarkError(ErrPerfUnavailable, fmt.Errorf("active profiling with perf is supported on linux only"))
	return perfResult{Message: err.Error()}, err
}
