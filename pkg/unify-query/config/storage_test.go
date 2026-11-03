// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLegacyConsulSwitchDoesNotChangeStorageConfiguration(t *testing.T) {
	for _, source := range []string{"", "consul", "redis"} {
		for _, enabled := range []any{nil, true, false, "false"} {
			candidate := viper.New()
			candidate.Set("storage.source", source)
			if enabled != nil {
				candidate.Set("consul.enabled", enabled)
			}
			require.NoError(t, validateStorageConfig(candidate))
		}
	}
}

func TestRedisStorageRejectsEmptyPrefixBeforeReload(t *testing.T) {
	viper.Reset()
	previous := CustomConfigFilePath
	t.Cleanup(func() { viper.Reset(); CustomConfigFilePath = previous })
	CustomConfigFilePath = filepath.Join(t.TempDir(), "config.yaml")
	initial := "storage:\n  source: redis\nredis:\n  kv_base_path: test:uq\n"
	require.NoError(t, os.WriteFile(CustomConfigFilePath, []byte(initial), 0600))
	require.NoError(t, InitConfigWithWriter(io.Discard))
	require.NoError(t, os.WriteFile(CustomConfigFilePath, []byte("storage:\n  source: redis\nredis:\n  kv_base_path: ''\n"), 0600))
	require.ErrorContains(t, ReloadConfigWithWriter(io.Discard), "nonempty redis.kv_base_path")
	require.Equal(t, "test:uq", viper.GetString("redis.kv_base_path"))
	require.ErrorContains(t, InitConfigWithWriter(io.Discard), "nonempty redis.kv_base_path")
}
