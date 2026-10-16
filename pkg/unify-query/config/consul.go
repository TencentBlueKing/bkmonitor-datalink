// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"fmt"

	"github.com/spf13/cast"
	"github.com/spf13/viper"
)

const ConsulEnabledConfigPath = "consul.enabled"

// ConsulEnabled preserves legacy deployments that do not configure the switch.
func ConsulEnabled() bool {
	return !viper.IsSet(ConsulEnabledConfigPath) || viper.GetBool(ConsulEnabledConfigPath)
}

func validateConsulConfig(candidate *viper.Viper) error {
	if !candidate.IsSet(ConsulEnabledConfigPath) {
		return nil
	}
	enabled, err := cast.ToBoolE(candidate.Get(ConsulEnabledConfigPath))
	if err != nil {
		return fmt.Errorf("consul.enabled must be a boolean")
	}
	if !enabled && candidate.GetString("storage.source") != "redis" {
		return fmt.Errorf("consul.enabled=false requires storage.source=redis")
	}
	if !enabled && candidate.IsSet("redis.kv_base_path") && candidate.GetString("redis.kv_base_path") == "" {
		return fmt.Errorf("consul.enabled=false requires a nonempty redis.kv_base_path")
	}
	return nil
}
