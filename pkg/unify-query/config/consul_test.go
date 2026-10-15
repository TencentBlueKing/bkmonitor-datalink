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

func TestConsulConfigurationCompatibility(t *testing.T) {
	for _, test := range []struct {
		name, source string
		enabled      any
		wantError    bool
	}{
		{name: "legacy default"},
		{name: "legacy consul", enabled: true, source: "consul"},
		{name: "migration", enabled: true, source: "redis"},
		{name: "redis only", enabled: false, source: "redis"},
		{name: "environment boolean", enabled: "false", source: "redis"},
		{name: "missing source", enabled: false, wantError: true},
		{name: "consul source disabled", enabled: false, source: "consul", wantError: true},
		{name: "invalid source", enabled: false, source: "redsi", wantError: true},
		{name: "invalid switch", enabled: "disabled", source: "redis", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := viper.New()
			if test.enabled != nil {
				candidate.Set(ConsulEnabledConfigPath, test.enabled)
			}
			candidate.Set("storage.source", test.source)
			err := validateConsulConfig(candidate)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConsulConfigurationRejectsInvalidReload(t *testing.T) {
	viper.Reset()
	previous := CustomConfigFilePath
	t.Cleanup(func() { viper.Reset(); CustomConfigFilePath = previous })
	CustomConfigFilePath = filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(CustomConfigFilePath, []byte("consul:\n  enabled: false\nstorage:\n  source: redis\n"), 0600))
	require.NoError(t, InitConfigWithWriter(io.Discard))
	require.False(t, ConsulEnabled())
	require.NoError(t, os.WriteFile(CustomConfigFilePath, []byte("consul:\n  enabled: false\nstorage:\n  source: consul\n"), 0600))
	require.ErrorContains(t, ReloadConfigWithWriter(io.Discard), "requires storage.source=redis")
	require.False(t, ConsulEnabled())
	require.Equal(t, "redis", viper.GetString("storage.source"))
}
