// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

func pipelineContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, define.ContextPipelineKey, &config.PipelineConfig{
		Option: map[string]interface{}{},
		ResultTableList: []*config.MetaResultTableConfig{
			{
				Option: map[string]interface{}{
					"dimension_values": []string{"k1", "k2", "k1/k2"},
				},
			},
		},
	})
	return ctx, cancel
}

type mockRedisKVImpl struct {
	T *testing.T
}

func (m *mockRedisKVImpl) ZAddBatch(k string, v map[string]float64) error {
	assert.Equal(m.T, "bkmonitor:metrics_0", k)
	assert.Equal(m.T, map[string]float64{
		"byte_total": float64(1670243190),
		"mem_pct":    float64(1670243190),
		"usage":      float64(1670243190),
	}, v)
	return nil
}

func (m *mockRedisKVImpl) HSetBatch(k string, v map[string]string) error {
	assert.Equal(m.T, "bkmonitor:metric_dimensions_0", k)
	items := map[string]etl.DimensionsEntity{
		"byte_total": {
			Dimensions: map[string]*etl.DimensionItem{
				"k1": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v1"},
				},
				"k1/k2": {
					LastUpdateTime: 1670243190,
					Values:         nil,
				},
			},
		},

		"mem_pct": {
			Dimensions: map[string]*etl.DimensionItem{
				"k1": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v2"},
				},
				"k2": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v2"},
				},
				"k1/k2": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v2/v2"},
				},
			},
		},

		"usage": {
			Dimensions: map[string]*etl.DimensionItem{
				"k1": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v1", "v3"},
				},
				"k2": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v4", "foo"},
				},
				"k3": {
					LastUpdateTime: 1670243190,
					Values:         nil,
				},
				"k1/k2": {
					LastUpdateTime: 1670243190,
					Values:         []string{"v1/foo"},
				},
			},
		},
	}

	for metric, content := range v {
		item := items[metric]
		var entity etl.DimensionsEntity
		assert.NoError(m.T, json.Unmarshal([]byte(content), &entity))
		for d := range item.Dimensions {
			sort.Strings(item.Dimensions[d].Values)
			sort.Strings(entity.Dimensions[d].Values)
			assert.Equal(m.T, item.Dimensions[d].Values, entity.Dimensions[d].Values)
			assert.Equal(m.T, item.Dimensions[d].LastUpdateTime, entity.Dimensions[d].LastUpdateTime)
		}
	}
	return nil
}

func (m *mockRedisKVImpl) HGetBatch(k string, v []string) ([]interface{}, error) {
	assert.Equal(m.T, "bkmonitor:metric_dimensions_0", k)
	metrics := map[string]bool{"mem_pct": true, "byte_total": true, "usage": true}
	for _, val := range v {
		assert.True(m.T, metrics[val])
	}
	ret := make([]interface{}, len(v))
	return ret, nil
}

func TestDimensionStoreGet(t *testing.T) {
	store := etl.NewDimensionStore()
	assert.False(t, store.Set("usage", etl.Label{Name: "label1", Value: "value1"}))
	assert.True(t, store.Set("usage", etl.Label{Name: "label1", Value: "value1"}))
	assert.False(t, store.Set("usage", etl.Label{Name: "label1", Value: "value2"}))
	m := store.GetOrMergeDimensions("usage", nil)

	sort.Strings(m["label1"].Values)
	assert.Equal(t, m["label1"].Values, []string{"value1", "value2"})

	m = store.GetOrMergeDimensions("usage", etl.DimensionMap{"label1": &etl.DimensionItem{Values: []string{"value3"}}})
	sort.Strings(m["label1"].Values)
	assert.Equal(t, m["label1"].Values, []string{"value1", "value2", "value3"})

	store = etl.NewDimensionStore()
	m = store.GetOrMergeDimensions("usage", etl.DimensionMap{"label1": &etl.DimensionItem{Values: []string{"value3"}}})
	assert.Equal(t, m["label1"].Values, []string{"value3"})

	store = etl.NewDimensionStore()
	store.Set("usage1", etl.Label{Name: "label1", Value: "value1"})
	store.Set("usage2", etl.Label{Name: "label2", Value: "value5"})
	m = store.GetOrMergeDimensions("usage1", etl.DimensionMap{"label1": &etl.DimensionItem{Values: []string{"value3"}}})
	sort.Strings(m["label1"].Values)
	assert.Equal(t, m["label1"].Values, []string{"value1", "value3"})
	m = store.GetOrMergeDimensions("usage2", nil)
	assert.Equal(t, m["label2"].Values, []string{"value5"})
}
