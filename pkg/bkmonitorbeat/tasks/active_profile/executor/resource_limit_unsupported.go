// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !linux

package executor

// resource_limit cgroup 只在 Linux 上由 libgse 创建，其它平台没有可移出的限制。
func moveOutOfResourceLimit(int) error { return nil }

func raiseOOMScore(int) error { return nil }
