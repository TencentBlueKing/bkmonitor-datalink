// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build windows

package active_profile

import "os"

// Active Profiling 只在 Linux 上采集，Windows 侧仅为可编译保留实现。
func tryLockFile(_ *os.File) (bool, error) { return true, nil }

func unlockFile(_ *os.File) error { return nil }
