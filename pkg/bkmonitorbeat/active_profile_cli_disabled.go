// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !activeprofile && !basetask

package main

// Active Profiling 只在 activeprofile / basetask build tag 下注册，默认构建不包含
// 轻量 CLI，也不链接采集实现。
func handleActiveProfileCLI() {}
