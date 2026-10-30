// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestActiveProfilingConfig(t *testing.T) {
	oldFilePath := FilePath
	oldConsulPathPrefix := ActiveProfilingConsulPathPrefix
	oldMaxDuration := ActiveProfilingWindowMaxDuration
	oldDelay := ActiveProfilingWindowDelay
	oldKeys := append([]string(nil), keys...)
	t.Cleanup(func() {
		FilePath = oldFilePath
		ActiveProfilingConsulPathPrefix = oldConsulPathPrefix
		ActiveProfilingWindowMaxDuration = oldMaxDuration
		ActiveProfilingWindowDelay = oldDelay
		keys = oldKeys
		viper.Reset()
	})

	configPath := filepath.Join(t.TempDir(), "bmw.yaml")
	configData := []byte("taskConfig:\n  activeProfiling:\n    consul:\n      pathPrefix: profiling/data_ids\n    window:\n      maxDuration: 2m\n      delay: 30s\n")
	require.NoError(t, os.WriteFile(configPath, configData, 0600))

	FilePath = configPath
	viper.Reset()
	require.NoError(t, loadConfig())
	require.Equal(t, "profiling/data_ids", ActiveProfilingConsulPathPrefix)
	require.Equal(t, 2*time.Minute, ActiveProfilingWindowMaxDuration)
	require.Equal(t, 30*time.Second, ActiveProfilingWindowDelay)
}
