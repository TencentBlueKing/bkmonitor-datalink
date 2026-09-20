// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import "github.com/go-redis/redis/v8"

// stubCmdable satisfies the client interface for the construction tests, which
// never issue a command.
type stubCmdable struct{ redis.Cmdable }
