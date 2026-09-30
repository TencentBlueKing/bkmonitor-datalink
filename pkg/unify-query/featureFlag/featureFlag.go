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
	"bytes"
	"context"
	stdjson "encoding/json"
	"fmt"
	"sync"
	"time"

	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/ffuser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var (
	featureFlag      *FeatureFlag
	once             sync.Once
	clientAccessLock sync.RWMutex
)

// WithClientLock 串行替换 go-feature-flag 全局客户端；Variation 调用会在替换完成后继续。
func WithClientLock(fn func() error) error {
	clientAccessLock.Lock()
	defer clientAccessLock.Unlock()
	return fn()
}

// FeatureFlag
type FeatureFlag struct {
	lock  *sync.RWMutex
	flags []byte
}

type snapshotRetriever struct {
	flags []byte
}

func (r snapshotRetriever) Retrieve(context.Context) ([]byte, error) {
	return r.flags, nil
}

func validateFeatureFlagSnapshot(data []byte) error {
	if !stdjson.Valid(data) {
		return fmt.Errorf("invalid feature flag JSON")
	}

	var flags map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &flags); err != nil || flags == nil {
		return fmt.Errorf("feature flag snapshot must be a JSON object")
	}
	for name, rawFlag := range flags {
		var flag struct {
			Variations map[string]stdjson.RawMessage `json:"variations"`
		}
		if err := stdjson.Unmarshal(rawFlag, &flag); err != nil {
			return fmt.Errorf("invalid feature flag %q: %w", name, err)
		}
		if len(flag.Variations) == 0 {
			return fmt.Errorf("feature flag %q must define variations", name)
		}
	}

	client, err := ffclient.New(ffclient.Config{
		Context:         context.Background(),
		PollingInterval: time.Hour,
		Retriever:       snapshotRetriever{flags: data},
		FileFormat:      "json",
	})
	if err != nil {
		return fmt.Errorf("invalid feature flag snapshot: %w", err)
	}
	client.Close()
	return nil
}

// ReloadFeatureFlags
func ReloadFeatureFlags(data []byte) error {
	_, err := ReloadFeatureFlagsIfChanged(data)
	return err
}

// ReloadFeatureFlagsIfChanged 更新 Retriever 的配置快照，并返回内容是否变化。
// 调用方可据此同步刷新 go-feature-flag 的内部缓存，避免仅等待轮询周期。
func ReloadFeatureFlagsIfChanged(data []byte) (bool, error) {
	if data == nil {
		return false, nil
	}
	if err := validateFeatureFlagSnapshot(data); err != nil {
		return false, err
	}
	featureFlag.lock.Lock()
	defer featureFlag.lock.Unlock()
	if bytes.Equal(featureFlag.flags, data) {
		return false, nil
	}
	featureFlag.flags = append([]byte(nil), data...)
	return true, nil
}

// Print
func Print() string {
	return string(getFeatureFlags())
}

// StringVariation
func StringVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue string) string {
	clientAccessLock.RLock()
	defer clientAccessLock.RUnlock()
	res, err := ffclient.StringVariation(flagKey, user, defaultValue)
	if err != nil {
		_ = metadata.NewMessage(
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
	clientAccessLock.RLock()
	defer clientAccessLock.RUnlock()
	res, err := ffclient.IntVariation(flagKey, user, defaultValue)
	if err != nil {
		return defaultValue
	}
	return res
}

// BoolVariation
func BoolVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue bool) bool {
	clientAccessLock.RLock()
	defer clientAccessLock.RUnlock()
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
		log.Debugf(ctx, "%s", string(info))
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
