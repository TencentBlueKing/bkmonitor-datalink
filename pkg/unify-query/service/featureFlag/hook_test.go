// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package featureFlag

import (
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigUsesStorageSource(t *testing.T) {
	oldStorageSource := viper.Get(StorageSourceConfigPath)
	oldDataSource := DataSource
	t.Cleanup(func() {
		viper.Set(StorageSourceConfigPath, oldStorageSource)
		DataSource = oldDataSource
	})

	viper.Set(StorageSourceConfigPath, "redis")
	LoadConfig()

	if DataSource != "redis" {
		t.Fatalf("expected FeatureFlag source to follow storage.source, got %q", DataSource)
	}
}
