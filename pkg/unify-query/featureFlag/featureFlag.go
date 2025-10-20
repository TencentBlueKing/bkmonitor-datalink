// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"
	"sync"

	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/ffuser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var (
	featureFlag *FeatureFlag
	once        sync.Once
)

// FeatureFlag
type FeatureFlag struct {
	lock  *sync.RWMutex
	flags []byte
}

// ReloadFeatureFlags
func ReloadFeatureFlags(data []byte) error {
	if data == nil {
		return nil
	}
	featureFlag.lock.Lock()
	defer featureFlag.lock.Unlock()
	featureFlag.flags = data
	return nil
}

// Print
func Print() string {
	return string(getFeatureFlags())
}

// StringVariation
func StringVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue string) string {
	res, err := ffclient.StringVariation(flagKey, user, defaultValue)
	if err != nil {
		_ = metadata.Sprintf(
			metadata.MsgFeatureFlag,
			"特性开关获取失败 flag_key: %s, user: %s, default_value: %s, error: %s",
			flagKey, user.GetKey(), defaultValue, err.Error(),
		).Error(ctx, err)
		return defaultValue
	}
	return res
}

// IntVariation
func IntVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue int) int {
	res, err := ffclient.IntVariation(flagKey, user, defaultValue)
	if err != nil {
		return defaultValue
	}
	return res
}

// BoolVariation
func BoolVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue bool) bool {
	res, err := ffclient.BoolVariation(flagKey, user, defaultValue)
	if err != nil {
		return defaultValue
	}
	return res
}

// getFeatureFlags
func getFeatureFlags() []byte {
	featureFlag.lock.RLock()
	defer featureFlag.lock.RUnlock()
	return featureFlag.flags
}

// setEvent
func setEvent(ctx context.Context, events []exporter.FeatureEvent) error {
	for _, event := range events {
		info, err := json.Marshal(event)
		if err != nil {
			return err
		}
		log.Debugf(ctx, string(info))
	}
	return nil
}

// init
func init() {
	once.Do(func() {
		featureFlag = &FeatureFlag{
			lock:  new(sync.RWMutex),
			flags: []byte("{}"),
		}
	})
}
