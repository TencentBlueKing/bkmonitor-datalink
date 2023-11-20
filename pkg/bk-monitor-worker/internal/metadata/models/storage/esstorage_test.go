// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var ess = ESStorage{
	TableID:           "test_table",
	DateFormat:        "%Y%m%d",
	SliceSize:         1,
	SliceGap:          1440,
	Retention:         7,
	WarmPhaseDays:     3,
	WarmPhaseSettings: `{"allocation_attr_name": "tag", "allocation_attr_value": "cold2", "allocation_type": "include"}`,
	TimeZone:          0,
	IndexSettings:     `{"number_of_shards":2,"number_of_replicas":1}`,
	MappingSettings:   `{"dynamic_templates":[{"strings_as_keywords":{}}]}`,
	StorageClusterID:  1234,
	esClient:          &elasticsearch.Elasticsearch{},
}

// TestESStorage_IndexBody 从db中构造index的body
func TestESStorage_IndexBody(t *testing.T) {
	config.InitConfig()
	patchDBSession := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		db, err := gorm.Open("mysql", fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?&parseTime=True&loc=Local",
			config.TestStorageMysqlUser,
			config.TestStorageMysqlPassword,
			config.TestStorageMysqlHost,
			config.TestStorageMysqlPort,
			config.TestStorageMysqlDbName,
		))
		assert.Nil(t, err)
		return &mysql.DBSession{DB: db}
	})
	patchESVersion := gomonkey.ApplyFunc(ESStorage.GetEsVersion, func(storage ESStorage) string {
		return "7"
	})
	defer patchDBSession.Reset()
	defer patchESVersion.Reset()

	// 初始化测试数据
	dbSession := mysql.GetDBSession()
	dbSession.DB.Where("table_id = ?", ess.TableID).Delete(&resulttable.ResultTableField{})
	dbSession.DB.Where("table_id = ?", ess.TableID).Delete(&resulttable.ResultTableFieldOption{})
	// 创建field
	dbSession.DB.Create(&resulttable.ResultTableField{
		TableID:        ess.TableID,
		FieldName:      "event_name",
		FieldType:      "string",
		Tag:            "dimension",
		IsConfigByUser: true,
		Creator:        "system",
		CreateTime:     time.Now(),
		LastModifyUser: "system",
		LastModifyTime: time.Now(),
		IsDisabled:     false,
	})
	dbSession.DB.Create(&resulttable.ResultTableField{
		TableID:        ess.TableID,
		FieldName:      "time",
		FieldType:      "timestamp",
		Tag:            "timestamp",
		IsConfigByUser: true,
		Creator:        "system",
		CreateTime:     time.Now(),
		LastModifyUser: "system",
		LastModifyTime: time.Now(),
		IsDisabled:     false,
	})
	dbSession.DB.Create(&resulttable.ResultTableField{
		TableID:        ess.TableID,
		FieldName:      "event",
		FieldType:      "object",
		Tag:            "dimension",
		IsConfigByUser: true,
		Creator:        "system",
		CreateTime:     time.Now(),
		LastModifyUser: "system",
		LastModifyTime: time.Now(),
		IsDisabled:     false,
	})
	// 创建fieldOption
	dbSession.DB.Create(&resulttable.ResultTableFieldOption{
		OptionBase: models.OptionBase{
			ValueType:  "string",
			Value:      "date_nanos",
			Creator:    "system",
			CreateTime: time.Now(),
		},
		TableID:   ess.TableID,
		FieldName: "time",
		Name:      "es_type",
	})
	dbSession.DB.Create(&resulttable.ResultTableFieldOption{
		OptionBase: models.OptionBase{
			ValueType:  "string",
			Value:      "epoch_millis",
			Creator:    "system",
			CreateTime: time.Now(),
		},
		TableID:   ess.TableID,
		FieldName: "time",
		Name:      "es_format",
	})
	dbSession.DB.Create(&resulttable.ResultTableFieldOption{
		OptionBase: models.OptionBase{
			ValueType:  "string",
			Value:      "object",
			Creator:    "system",
			CreateTime: time.Now(),
		},
		TableID:   ess.TableID,
		FieldName: "event",
		Name:      "es_properties",
	})
	dbSession.DB.Create(&resulttable.ResultTableFieldOption{
		OptionBase: models.OptionBase{
			ValueType:  "dict",
			Value:      `{"content":{"type":"text"},"count":{"type":"integer"}}`,
			Creator:    "system",
			CreateTime: time.Now(),
		},
		TableID:   ess.TableID,
		FieldName: "event",
		Name:      "es_type",
	})
	dbSession.DB.Create(&resulttable.ResultTableFieldOption{
		OptionBase: models.OptionBase{
			ValueType:  "string",
			Value:      "keyword",
			Creator:    "system",
			CreateTime: time.Now(),
		},
		TableID:   ess.TableID,
		FieldName: "event_name",
		Name:      "es_type",
	})
	// 构造索引body
	body, err := ess.IndexBody()
	assert.Nil(t, err)
	var dbBody map[string]interface{}
	var targetBody map[string]interface{}
	target := `{"settings":{"number_of_shards":2,"number_of_replicas":1},"mappings":{"dynamic_templates":[{"strings_as_keywords":{}}],"properties":{"time":{"type":"date_nanos","format":"epoch_millis"},"event":{"properties":"object","type":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"}}}}`
	err = jsonx.Unmarshal(body, &dbBody)
	assert.Nil(t, err)
	err = jsonx.UnmarshalString(target, &targetBody)
	assert.Nil(t, err)
	assert.True(t, reflect.DeepEqual(dbBody, targetBody))
}

func TestESStorage_isIndexExist(t *testing.T) {
	patchGetIndices := gomonkey.ApplyFunc(elasticsearch.Elasticsearch.GetIndices, func(es elasticsearch.Elasticsearch, indices []string) (*elasticsearch.Response, error) {
		existIndex := fmt.Sprintf("v2_%s_*", ess.TableID)
		for _, index := range indices {
			if index == existIndex {
				reader := strings.NewReader(`{"v2_test_table_20230901_0":{"aliases":{"test_table_20230914_read":{},"test_table_20230915_read":{},"test_table_20230916_read":{}},"mappings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}},{"message_full":{"match":"message_full","mapping":{"fields":{"keyword":{"ignore_above":2048,"type":"keyword"}},"type":"text"}}},{"message":{"match":"message","mapping":{"type":"text"}}},{"strings":{"match_mapping_type":"string","mapping":{"type":"keyword"}}}],"properties":{"dimensions":{"dynamic":"true","properties":{"d24":{"type":"keyword"},"locatio2n":{"type":"keyword"},"location":{"type":"keyword"},"location2":{"type":"keyword"},"modul3e":{"type":"keyword"},"module":{"type":"keyword"},"new_field_a":{"type":"keyword"}}},"event":{"properties":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"},"target":{"type":"keyword"},"time":{"type":"date_nanos","format":"epoch_millis"}}},"settings":{"index":{"routing":{"allocation":{"include":{"_tier_preference":"data_content","tag":"cold2"}}},"number_of_shards":"1","provided_name":"v2_test_table_20230901_0","creation_date":"1695197103630","number_of_replicas":"1","uuid":"JX2eU355SGS8gduKWNcRzg","version":{"created":"7140099"}}}},"v2_test_table_20230919_0":{"aliases":{"test_table_20230919_read":{},"test_table_20230920_read":{}},"mappings":{"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}],"properties":{"dimensions":{"type":"object","dynamic":"true"},"event":{"properties":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"},"target":{"type":"keyword"},"target2":{"type":"keyword"},"time":{"type":"date_nanos","format":"epoch_millis"}}},"settings":{"index":{"routing":{"allocation":{"include":{"_tier_preference":"data_content"}}},"number_of_shards":"2","provided_name":"v2_test_table_20230919_0","creation_date":"1695124157591","number_of_replicas":"1","uuid":"do9bXpTpSuifYtAXcKhVhA","version":{"created":"7140099"}}}},"v2_test_table_20230919_1":{"aliases":{"test_table_20230919_read":{},"test_table_20230920_read":{},"test_table_20230921_read":{},"write_20230919_test_table":{}},"mappings":{"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}],"properties":{"dimensions":{"type":"object","dynamic":"true"},"event":{"properties":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"},"target":{"type":"keyword"},"target2":{"type":"keyword"},"time":{"type":"date_nanos","format":"epoch_millis"}}},"settings":{"index":{"routing":{"allocation":{"include":{"_tier_preference":"data_content"}}},"number_of_shards":"2","provided_name":"v2_test_table_20230919_1","creation_date":"1695124429445","number_of_replicas":"1","uuid":"cd0z-CO4QJaKZPs1DP32fg","version":{"created":"7140099"}}}},"v2_test_table_20230920_0":{"aliases":{"test_table_20230920_read":{},"test_table_20230921_read":{},"write_20230920_test_table":{}},"mappings":{"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}],"properties":{"dimensions":{"dynamic":"true","properties":{"d24":{"type":"keyword"},"locatio2n":{"type":"keyword"},"modul3e":{"type":"keyword"}}},"event":{"properties":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"},"target":{"type":"keyword"},"target2":{"type":"keyword"},"time":{"type":"date_nanos","format":"epoch_millis"}}},"settings":{"index":{"routing":{"allocation":{"include":{"_tier_preference":"data_content"}}},"number_of_shards":"2","provided_name":"v2_test_table_20230920_0","creation_date":"1695196479506","number_of_replicas":"1","uuid":"oRN1Hu86RX63jvnXH-i72w","version":{"created":"7140099"}}}},"v2_test_table_20230921_0":{"aliases":{"test_table_20230921_read":{},"test_table_20230922_read":{},"write_20230921_test_table":{},"write_20230922_test_table":{}},"mappings":{"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}],"properties":{"dimensions":{"type":"object","dynamic":"true"},"event":{"properties":{"content":{"type":"text"},"count":{"type":"integer"}}},"event_name":{"type":"keyword"},"target":{"type":"keyword"},"target2":{"type":"keyword"},"time":{"type":"date_nanos","format":"epoch_millis"}}},"settings":{"index":{"routing":{"allocation":{"include":{"_tier_preference":"data_content"}}},"number_of_shards":"2","provided_name":"v2_test_table_20230921_0","creation_date":"1695263345983","number_of_replicas":"1","uuid":"AZwExKSQS4SELdi1ecf0Pw","version":{"created":"7140099"}}}}}`)
				return &elasticsearch.Response{StatusCode: 200, Body: ioutils.NewReadCloserWrapper(reader, func() error { return nil })}, nil
			}
		}
		return nil, elasticsearch.NotFoundErr
	})
	defer patchGetIndices.Reset()

	exist, err := ess.isIndexExist(context.TODO(), ess.searchFormatV2(), ess.IndexReV2())
	assert.Nil(t, err)
	assert.True(t, exist)
	exist, err = ess.isIndexExist(context.TODO(), ess.searchFormatV1(), ess.IndexReV1())
	assert.Nil(t, err)
	assert.False(t, exist)
}
