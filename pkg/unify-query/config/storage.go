// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"fmt"
	"github.com/spf13/viper"
)

func validateStorageConfig(candidate *viper.Viper) error {
	if candidate.GetString("storage.source") == "redis" &&
		candidate.IsSet("redis.kv_base_path") && candidate.GetString("redis.kv_base_path") == "" {
		return fmt.Errorf("storage.source=redis requires a nonempty redis.kv_base_path")
	}
	return nil
}
