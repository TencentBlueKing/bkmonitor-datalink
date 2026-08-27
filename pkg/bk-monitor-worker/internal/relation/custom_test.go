// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

// MockRelationDataIDReporter simulates the configured shared relation target.
type MockRelationDataIDReporter struct {
	WriteFunc func(ctx context.Context, ts ...prompb.TimeSeries) error
	CloseFunc func(ctx context.Context) error
}

func (m *MockRelationDataIDReporter) Write(ctx context.Context, ts ...prompb.TimeSeries) error {
	if m.WriteFunc != nil {
		return m.WriteFunc(ctx, ts...)
	}
	return nil
}

func (m *MockRelationDataIDReporter) Close(ctx context.Context) error {
	if m.CloseFunc != nil {
		return m.CloseFunc(ctx)
	}
	return nil
}

func TestReportCustomRelation(t *testing.T) {
	mocker.InitTestDBConfig("../../dist/bmw.yaml")
	originalRelationDataID := config.RelationDataID
	config.RelationDataID = 1573946
	t.Cleanup(func() {
		config.RelationDataID = originalRelationDataID
	})

	db := mysql.GetDBSession().DB
	table := &relation.CustomRelationStatus{}
	db.DropTable(table).CreateTable(table)

	now := time.Now()

	ctx := context.Background()
	t.Run("空记录测试", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethodFunc(
			relation.CustomRelationStatusQuerySet{},
			"All",
			func(ret *[]relation.CustomRelationStatus) error {
				*ret = []relation.CustomRelationStatus{}
				return nil
			},
		)

		err := ReportCustomRelation(ctx, nil)
		assert.NoError(t, err)
	})

	t.Run("正常数据上报测试", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		testData := []relation.CustomRelationStatus{
			{
				ID:           1,
				Namespace:    "test-namespace-1",
				Name:         "test-relation-1",
				Labels:       `{"bk_biz_id": "1", "env": "test"}`,
				FromResource: "service",
				ToResource:   "pod",
			},
			{
				ID:           2,
				Namespace:    "test-namespace-1",
				Name:         "test-relation-2",
				Labels:       `{"bk_biz_id": "1", "cluster": "test-cluster"}`,
				FromResource: "pod",
				ToResource:   "container",
			},
			{
				ID:           3,
				Namespace:    "test-namespace-2",
				Name:         "test-relation-3",
				Labels:       `{"bk_biz_id": "2", "region": "test-region"}`,
				FromResource: "node",
				ToResource:   "pod",
			},
		}

		for _, data := range testData {
			data.CreateTime = now
			data.UpdateTime = now
			data.UID = fmt.Sprintf("%d", data.ID)
			err := db.Create(&data).Error
			assert.NoError(t, err)
		}

		var reportedMetrics []prompb.TimeSeries

		mockReporter := &MockRelationDataIDReporter{
			WriteFunc: func(ctx context.Context, ts ...prompb.TimeSeries) error {
				reportedMetrics = append(reportedMetrics, ts...)
				return nil
			},
		}

		patches.ApplyFunc(
			remote.NewRelationDataIDReporter,
			func(dataID uint, remoteWriteURL string, headers map[string]string) (remote.RelationDataIDReporter, error) {
				assert.Equal(t, uint(1573946), dataID)
				return mockReporter, nil
			},
		)

		err := ReportCustomRelation(ctx, nil)
		assert.NoError(t, err)

		assert.Equal(t, 3, len(reportedMetrics)) // 两个 namespace 的指标写入同一目标
	})

	t.Run("指标上报错误测试", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		testData := []relation.CustomRelationStatus{
			{
				ID:           1,
				Namespace:    "test-namespace",
				Name:         "test-relation",
				Labels:       `{"bk_biz_id": "1"}`,
				FromResource: "service",
				ToResource:   "pod",
			},
		}

		for _, data := range testData {
			data.CreateTime = now
			data.UpdateTime = now
			data.UID = fmt.Sprintf("%d", data.ID)
			err := db.Create(&data).Error
			assert.NoError(t, err)
		}

		mockReporter := &MockRelationDataIDReporter{
			WriteFunc: func(ctx context.Context, ts ...prompb.TimeSeries) error {
				return errors.New("report error")
			},
		}

		patches.ApplyFunc(
			remote.NewRelationDataIDReporter,
			func(dataID uint, remoteWriteURL string, headers map[string]string) (remote.RelationDataIDReporter, error) {
				return mockReporter, nil
			},
		)

		err := ReportCustomRelation(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "report error")
	})

	t.Run("RelationDataIDReporter创建错误测试", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		testData := []relation.CustomRelationStatus{
			{
				ID:           1,
				Namespace:    "test-namespace",
				Name:         "test-relation",
				Labels:       `{"bk_biz_id": "1"}`,
				FromResource: "service",
				ToResource:   "pod",
			},
		}

		for _, data := range testData {
			data.CreateTime = now
			data.UpdateTime = now
			data.UID = fmt.Sprintf("%d", data.ID)
			err := db.Create(&data).Error
			assert.NoError(t, err)
		}

		patches.ApplyFunc(
			remote.NewRelationDataIDReporter,
			func(dataID uint, remoteWriteURL string, headers map[string]string) (remote.RelationDataIDReporter, error) {
				return nil, errors.New("create reporter error")
			},
		)

		err := ReportCustomRelation(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "create reporter error")
	})

	t.Run("未配置RelationDataID跳过测试", func(t *testing.T) {
		config.RelationDataID = 0
		defer func() {
			config.RelationDataID = 1573946
		}()

		err := ReportCustomRelation(ctx, nil)
		assert.NoError(t, err)
	})
}

func TestCustomTsPool(t *testing.T) {
	// 测试sync.Pool的正确使用
	t.Run("池重用测试", func(t *testing.T) {
		// 获取一个空的TimeSeries切片
		ts1 := customTsPool.Get().([]prompb.TimeSeries)
		assert.Equal(t, 0, len(ts1))

		// 添加一些数据
		ts1 = append(ts1, prompb.TimeSeries{})
		assert.Equal(t, 1, len(ts1))

		// 放回池中
		ts1 = ts1[:0] // 清空切片但保留容量
		customTsPool.Put(ts1)

		// 再次获取，应该重用之前的切片
		ts2 := customTsPool.Get().([]prompb.TimeSeries)
		assert.Equal(t, 0, len(ts2))
		// 检查是否重用了容量
		assert.True(t, cap(ts2) >= 1)
	})
}

func TestReportCustomRelationByNamespaceSkipsWhenReportingIsDisabled(t *testing.T) {
	originalDataID := config.RelationDataID
	originalRemoteWriteURL := config.PromRemoteWriteUrl
	t.Cleanup(func() {
		config.RelationDataID = originalDataID
		config.PromRemoteWriteUrl = originalRemoteWriteURL
	})

	config.RelationDataID = 0
	config.PromRemoteWriteUrl = "http://remote-write"
	assert.NoError(t, ReportCustomRelationByNamespace(context.Background(), "bkcc__7"))

	config.RelationDataID = 1573946
	config.PromRemoteWriteUrl = ""
	assert.NoError(t, ReportCustomRelationByNamespace(context.Background(), "bkcc__7"))
}
