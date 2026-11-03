// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

//go:build race

// Package racedetector says whether the binary was built with the race
// detector. A test that counts allocations, or bytes allocated per call,
// reads the instrumentation rather than the code under it: the detector adds
// allocations of its own and its sync.Pool drops a quarter of what is put
// back, so such an assertion is checked only without it.
package racedetector

// Enabled is true in a binary built with -race.
const Enabled = true
