// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

//go:build !unix

package redistest

// Without a way to ask whether a pid is alive, no owner is taken for gone
// and nothing is reaped.
func processGone(int) bool { return false }

func killServerProcess(int, string) bool { return false }
