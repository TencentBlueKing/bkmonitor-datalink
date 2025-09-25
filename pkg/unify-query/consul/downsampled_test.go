// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var (
	database        = "demo"
	rpList          = []string{"5m", "1h", "12h"}
	measurement     = "__all__"
	fieldList       = []string{"value"}
	aggregationList = []string{"count", "sum", "mean"}
	targetSourceRp  = map[string]string{
		"5m": "autogen",
		"1h": "5m",
	}
)

// mockConsulData
func mockConsulData(t *testing.T) (*gomock.Controller, *gostub.Stubs) {
	ctrl := gomock.NewController(t)
	data := make(api.KVPairs, 0)
	MetadataPath = "test/metadata"
	lastModifyTime := "2022-04-15 18:38:47+0800"

	dkey := fmt.Sprintf("%s/%s/%s/cq", MetadataPath, downsampledPath, database)
	data = append(data, &api.KVPair{
		Key:   dkey,
		Value: []byte(fmt.Sprintf(`{"tag_name":"","tag_value":[""],"enable":true,"last_modify_time":"%s"}`, lastModifyTime)),
	})

	for _, rp := range rpList {
		resolution, _ := time.ParseDuration(rp)
		val := fmt.Sprintf(`{"duration":"720h","resolution":%d,"last_modify_time":"%s"}`, int64(resolution.Seconds()), lastModifyTime)
		key := fmt.Sprintf("%s/%s/%s/rp/%s", MetadataPath, downsampledPath, database, rp)
		data = append(data, &api.KVPair{
			Key:   key,
			Value: []byte(val),
		})
	}

	// 添加默认rp
	data = append(data, &api.KVPair{
		Key:   fmt.Sprintf("%s/%s/%s/rp/%s", MetadataPath, downsampledPath, database, "default"),
		Value: []byte(`{"duration":"720h","resolution":1,"measurement":"new_rp"}`),
	})

	for targetRp, sourceRp := range targetSourceRp {
		for _, aggregation := range aggregationList {
			for _, field := range fieldList {
				key := fmt.Sprintf("%s/%s/%s/cq/%s/%s/%s/%s",
					MetadataPath, downsampledPath, database, measurement, field, aggregation, targetRp,
				)
				val := fmt.Sprintf(`{"source_rp":"%s","last_modify_time":"%s"}`, sourceRp, lastModifyTime)
				data = append(data, &api.KVPair{
					Key:   key,
					Value: []byte(val),
				})
			}
		}
	}

	stubs := gostub.Stub(&GetDataWithPrefix, func(prefix string) (api.KVPairs, error) {
		return data, nil
	})
	return ctrl, stubs
}

// TestGetDownsampledInfo
func TestGetDownsampledInfo(t *testing.T) {
	var err error

	lastModifyTime := "2022-04-15 18:38:47+0800"
	log.InitTestLogger()
	ctrl, stubs := mockConsulData(t)
	defer stubs.Reset()
	defer ctrl.Finish()

	log.Infof(context.TODO(), "TestGetDownsampledInfo Start")
	_ = SetInstance(
		context.Background(), "", "downsampled-test", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", nil,
	)

	err = LoadDownsampledInfo()

	assert.Nil(t, err)
	downsampledDatabase := DownsampledDatabase{
		Database:       database,
		TagName:        "",
		TagValue:       []string{""},
		Enable:         true,
		LastModifyTime: lastModifyTime,
	}
	assert.Contains(t, DownsampledInfo.Databases, downsampledDatabase.Key())
	for _, rp := range rpList {
		duration, _ := time.ParseDuration(rp)
		policy := DownsampledRetentionPolicy{
			Database:       database,
			RpName:         rp,
			Duration:       "720h",
			Resolution:     int64(duration.Seconds()),
			LastModifyTime: lastModifyTime,
		}
		assert.Contains(t, DownsampledInfo.RetentionPolicies, policy.Key())
	}

	// 断言默认rp
	defaultRP := DownsampledRetentionPolicy{
		Database:    database,
		RpName:      "default",
		Measurement: "new_rp",
		Duration:    "720h",
		Resolution:  1,
	}
	assert.Equal(t, DownsampledInfo.DBMeasurementRPMap[defaultRP.TableIDKey()].RpName, defaultRP.RpName)

	assertCqs := make(map[string][]DownsampledContinuousQuery)
	for targetRp, sourceRp := range targetSourceRp {
		for _, aggregation := range aggregationList {
			for _, field := range fieldList {
				query := DownsampledContinuousQuery{
					Database:       database,
					Measurement:    measurement,
					Field:          field,
					Aggregation:    aggregation,
					RpName:         targetRp,
					SourceRp:       sourceRp,
					LastModifyTime: lastModifyTime,
				}
				if _, ok := assertCqs[query.Key()]; !ok {
					assertCqs[query.Key()] = make([]DownsampledContinuousQuery, 0)
				}
				assertCqs[query.Key()] = append(assertCqs[query.Key()], query)
			}
		}
	}
	//assert.Equal(t, DownsampledInfo.Measurements, map[string]map[string]interface{}{
	//	"demo": {
	//		"__all__": nil,
	//	},
	//})

	log.Infof(context.TODO(), "TestGetDownsampledInfo End")
}

// TestCheckDownsampledStatus
func TestCheckDownsampledStatus(t *testing.T) {
	var err error

	log.InitTestLogger()
	ctrl, stubs := mockConsulData(t)
	defer stubs.Reset()
	defer ctrl.Finish()

	assert.Nil(t, err)
}
