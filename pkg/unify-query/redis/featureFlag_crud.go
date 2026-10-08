// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
)

// AddFeatureFlag 在已有快照中新增单个开关，data 为完整的开关定义。
func (f *FeatureFlagClient) AddFeatureFlag(ctx context.Context, name string, data []byte) error {
	return f.modifyFeatureFlag(ctx, name, func(flags map[string]json.RawMessage) error {
		if _, exists := flags[name]; exists {
			return fmt.Errorf("feature flag %q already exists", name)
		}
		flags[name] = json.RawMessage(data)
		return nil
	})
}

// UpdateFeatureFlag 替换已有单个开关的完整定义。
func (f *FeatureFlagClient) UpdateFeatureFlag(ctx context.Context, name string, data []byte) error {
	return f.modifyFeatureFlag(ctx, name, func(flags map[string]json.RawMessage) error {
		if _, exists := flags[name]; !exists {
			return fmt.Errorf("feature flag %q does not exist", name)
		}
		flags[name] = json.RawMessage(data)
		return nil
	})
}

// DeleteFeatureFlag 删除已有单个开关；最后一个开关删除后保留空快照，避免重新回填。
func (f *FeatureFlagClient) DeleteFeatureFlag(ctx context.Context, name string) error {
	return f.modifyFeatureFlag(ctx, name, func(flags map[string]json.RawMessage) error {
		if _, exists := flags[name]; !exists {
			return fmt.Errorf("feature flag %q does not exist", name)
		}
		delete(flags, name)
		return nil
	})
}

func (f *FeatureFlagClient) modifyFeatureFlag(ctx context.Context, name string, modify func(map[string]json.RawMessage) error) error {
	if f.client == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	if name == "" {
		return fmt.Errorf("feature flag name must not be empty")
	}
	key := f.GetFeatureFlagsPath()
	var snapshot []byte
	err := f.client.Watch(ctx, func(tx *goRedis.Tx) error {
		data, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, goRedis.Nil) {
			return fmt.Errorf("feature flag snapshot does not exist; wait for UQ migration or initialize with set-feature-flags")
		}
		if err != nil {
			return err
		}
		var flags map[string]json.RawMessage
		if err := json.Unmarshal(data, &flags); err != nil || flags == nil {
			return fmt.Errorf("stored feature flag snapshot must be a JSON object")
		}
		if err := modify(flags); err != nil {
			return err
		}
		snapshot, err = json.Marshal(flags)
		if err != nil {
			return err
		}
		if err := featureFlag.ValidateFeatureFlagSnapshot(snapshot); err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe goRedis.Pipeliner) error {
			pipe.Set(ctx, key, snapshot, 0)
			return nil
		})
		return err
	}, key)
	if errors.Is(err, goRedis.TxFailedErr) {
		return fmt.Errorf("feature flag snapshot changed concurrently; retry the operation: %w", err)
	}
	if err != nil {
		return fmt.Errorf("failed to change feature flag %q: %w", name, err)
	}
	f.publishFeatureFlags(ctx, snapshot)
	return nil
}
