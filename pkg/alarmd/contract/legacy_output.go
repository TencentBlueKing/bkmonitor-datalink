// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// LegacyOutputContext is internal frozen configuration for the local Python
// protocol adapter. It is never part of the native TriggerEvent wire.
type LegacyOutputContext struct {
	DynamicDimensions bool            `json:"dynamic_dimensions,omitempty"`
	Strategy          json.RawMessage `json:"strategy"`
	DimensionFields   []string        `json:"dimension_fields"`
	ItemID            string          `json:"item_id"`
}

type LegacyEventContext struct {
	Configuration     *FrozenLegacyOutput
	AnomalyTimestamps []int64
}

// FrozenLegacyOutput can be shared by every event from a compiled Plan. No
// accessor exposes writable backing storage; the strategy is copied only when
// the converter adds a distinct configuration to a batch request.
type FrozenLegacyOutput struct {
	dynamicDimensions bool
	strategy          string
	key               string
	dimensionFields   []string
	itemID            string
}

func FreezeLegacyOutput(c *LegacyOutputContext) *FrozenLegacyOutput {
	if c == nil {
		return nil
	}
	digest := sha256.Sum256(c.Strategy)
	return &FrozenLegacyOutput{dynamicDimensions: c.DynamicDimensions, strategy: string(c.Strategy), key: hex.EncodeToString(digest[:]), dimensionFields: append([]string{}, c.DimensionFields...), itemID: c.ItemID}
}
func (c *FrozenLegacyOutput) StrategyJSON() json.RawMessage { return json.RawMessage(c.strategy) }
func (c *FrozenLegacyOutput) StrategyKey() string           { return c.key }
func (c *FrozenLegacyOutput) DimensionFields() []string {
	return append([]string{}, c.dimensionFields...)
}
func (c *FrozenLegacyOutput) DynamicDimensions() bool { return c.dynamicDimensions }
func (c *FrozenLegacyOutput) ItemID() string          { return c.itemID }
func (c *FrozenLegacyOutput) SizeBytes() int {
	size := len(c.strategy) + len(c.key) + len(c.itemID) + len(c.dimensionFields)*16
	for _, field := range c.dimensionFields {
		size += len(field)
	}
	return size
}

func (c *LegacyOutputContext) Validate() error {
	if c == nil {
		return nil
	}
	if c.DimensionFields == nil || c.ItemID == "" {
		return fmt.Errorf("incomplete legacy output metadata")
	}
	var strategy struct {
		ID         int64 `json:"id"`
		BusinessID int64 `json:"bk_biz_id"`
		UpdateTime int64 `json:"update_time"`
	}
	if err := json.Unmarshal(c.Strategy, &strategy); err != nil || strategy.ID <= 0 || strategy.BusinessID == 0 || strategy.UpdateTime <= 0 {
		return fmt.Errorf("invalid frozen legacy strategy")
	}
	return nil
}
