// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

//go:build !linux

package executor

import "errors"

type unsupportedResourceProbe struct {
	directoryUsageProbe
}

func newHostResourceProbe() resourceProbe {
	return unsupportedResourceProbe{}
}

func (unsupportedResourceProbe) MemoryAvailableBytes() (uint64, error) {
	return 0, errors.New("active profiling resource guard is only supported on linux")
}

func (unsupportedResourceProbe) MemoryLimitBytes() uint64 { return 0 }
