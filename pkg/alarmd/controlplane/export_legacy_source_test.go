// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

// LegacyStrategyMGetChunkForTest is how many strategy documents one MGET
// reads, so a test drives the boundary rather than restating the number.
const LegacyStrategyMGetChunkForTest = legacyStrategyMGetChunk
