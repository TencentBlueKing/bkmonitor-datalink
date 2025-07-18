// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestDataSourceSvc_ToJson(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	ds := &resulttable.DataSource{
		BkDataId:          99999,
		Token:             "9e679720296f4ad7abf5ad95ac0acbdf",
		DataName:          "test_data_source",
		DataDescription:   "data source for test",
		MqClusterId:       1007,
		MqConfigId:        21,
		EtlConfig:         "bk_standard_v2_event",
		IsCustomSource:    true,
		Creator:           "admin",
		CreateTime:        time.Time{},
		LastModifyUser:    "admin",
		LastModifyTime:    time.Time{},
		TypeLabel:         "event",
		SourceLabel:       "bk_monitor",
		IsEnable:          true,
		TransferClusterId: "default",
		SpaceTypeId:       "all",
		SpaceUid:          "",
	}

	dsoA := resulttable.DataSourceOption{
		OptionBase: models.OptionBase{
			ValueType: "bool",
			Value:     "true",
			Creator:   "system",
		},
		BkDataId: ds.BkDataId,
		Name:     "test_bool",
	}
	dsoB := resulttable.DataSourceOption{
		OptionBase: models.OptionBase{
			ValueType: "string",
			Value:     "string abc",
			Creator:   "system",
		},
		BkDataId: ds.BkDataId,
		Name:     "test_string",
	}
	kafkaTopic := &storage.KafkaTopicInfo{
		BkDataId:  ds.BkDataId,
		Topic:     "0bkmonitor_999990",
		Partition: 1,
	}
	rt := resulttable.ResultTable{
		TableId:        "test_data_source_table_id",
		IsCustomTable:  true,
		SchemaType:     "",
		DefaultStorage: models.StorageTypeES,
		IsEnable:       true,
		Label:          "others",
	}
	rtfA := resulttable.ResultTableField{
		TableID:        rt.TableId,
		FieldName:      "f1",
		FieldType:      models.ResultTableFieldTypeString,
		Description:    "f1 test",
		Tag:            models.ResultTableFieldTagDimension,
		IsConfigByUser: true,
	}
	rtfB := resulttable.ResultTableField{
		TableID:        rt.TableId,
		FieldName:      "f2",
		FieldType:      models.ResultTableFieldTypeBoolean,
		Description:    "f2 test",
		Tag:            models.ResultTableFieldTagDimension,
		IsConfigByUser: true,
	}
	dsrt := resulttable.DataSourceResultTable{
		BkDataId: ds.BkDataId,
		TableId:  rt.TableId,
	}

	// 初始化数据
	db := mysql.GetDBSession().DB
	db.Delete(&kafkaTopic, "bk_data_id=?", kafkaTopic.BkDataId)
	err := kafkaTopic.Create(db)
	assert.NoError(t, err)
	db.Delete(&ds)
	err = ds.Create(db)
	assert.Nil(t, err)

	db.Delete(&rt)
	err = rt.Create(db)

	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", rt.TableId)
	err = rtfA.Create(db)
	assert.NoError(t, err)
	err = rtfB.Create(db)
	assert.NoError(t, err)

	assert.Nil(t, err)
	db.Delete(&dsrt, "table_id=?", dsrt.TableId)
	err = dsrt.Create(db)
	assert.Nil(t, err)

	version := "7"
	schema := "http"
	cluster := storage.ClusterInfo{
		ClusterName: "test_es_0002",
		ClusterType: models.StorageTypeES,
		DomainName:  "127.0.0.1",
		Port:        9200,
		Schema:      &schema,
		ClusterID:   7,
		Version:     &version,
		Password:    "testpassword",
		Username:    "elasticsearch",
	}
	mqCluster := storage.ClusterInfo{
		ClusterName:      "kafka_default_test",
		ClusterID:        1007,
		DomainName:       "127.0.0.1",
		Port:             9096,
		ExtranetPort:     0,
		ClusterType:      models.StorageTypeKafka,
		IsDefaultCluster: true,
	}

	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err = cluster.Create(db)
	assert.NoError(t, err)

	db.Delete(&mqCluster, "cluster_name = ?", mqCluster.ClusterName)
	err = mqCluster.Create(db)
	assert.NoError(t, err)

	db.Delete(&resulttable.DataSourceOption{}, "bk_data_id = ?", ds.BkDataId)
	err = dsoA.Create(db)
	assert.NoError(t, err)
	err = dsoB.Create(db)
	assert.NoError(t, err)

	es := storage.ESStorage{
		TableID:           rt.TableId,
		WarmPhaseSettings: "{}",
		IndexSettings:     "{}",
		MappingSettings:   "{}",
		StorageClusterID:  cluster.ClusterID,
		NeedCreateIndex:   true,
	}
	db.Delete(&es, "table_id = ?", es.TableID)
	err = es.Create(db)
	assert.NoError(t, err)

	dsSvc := NewDataSourceSvc(ds)
	dsConfig, err := dsSvc.ToJson(true, true)
	assert.NoError(t, err)
	dsConfigJson, err := jsonx.MarshalString(dsConfig)
	// 	去除时间字段，避免影响比对
	re := regexp.MustCompile(`"create_time":\s?(?P<datetime>\d+),`)
	matchedList := re.FindAllStringSubmatch(dsConfigJson, -1)
	for _, s := range matchedList {
		dsConfigJson = strings.ReplaceAll(dsConfigJson, s[1], "0")
	}
	targetJson := fmt.Sprintf(`{"bk_data_id":99999,"data_id":99999,"data_name":"test_data_source","etl_config":"bk_standard_v2_event","is_platform_data_id":false,"mq_config":{"auth_info":{"password":"","username":""},"batch_size":null,"cluster_config":{"domain_name":"127.0.0.1","port":9096,"extranet_domain_name":"","extranet_port":0,"schema":null,"is_ssl_verify":false,"ssl_verification_mode":"","ssl_insecure_skip_verify":false,"ssl_certificate_authorities":"","ssl_certificate":"","ssl_certificate_key":"","raw_ssl_certificate_authorities":"","raw_ssl_certificate":"","raw_ssl_certificate_key":"","cluster_id":1007,"cluster_name":"kafka_default_test","version":null,"custom_option":"","registered_system":"","creator":"","create_time":0,"last_modify_user":"","is_default_cluster":true},"cluster_type":"kafka","consume_rate":null,"flush_interval":null,"storage_config":{"partition":1,"topic":"0bkmonitor_999990"}},"option":{"test_bool":true,"test_string":"string abc"},"result_table_list":[{"bk_biz_id":0,"field_list":[{"alias_name":"","default_value":null,"description":"f1 test","field_name":"f1","is_config_by_user":true,"is_disabled":false,"option":{},"tag":"dimension","type":"string","unit":""},{"alias_name":"","default_value":null,"description":"f2 test","field_name":"f2","is_config_by_user":true,"is_disabled":false,"option":{},"tag":"dimension","type":"boolean","unit":""}],"option":{},"result_table":"test_data_source_table_id","schema_type":"","shipper_list":[{"cluster_config":{"domain_name":"127.0.0.1","port":9200,"extranet_domain_name":"","extranet_port":0,"schema":"http","is_ssl_verify":false,"ssl_verification_mode":"","ssl_insecure_skip_verify":false,"ssl_certificate_authorities":"","ssl_certificate":"","ssl_certificate_key":"","raw_ssl_certificate_authorities":"","raw_ssl_certificate":"","raw_ssl_certificate_key":"","cluster_id":%v,"cluster_name":"test_es_0002","version":"7","custom_option":"","registered_system":"","creator":"","create_time":0,"last_modify_user":"","is_default_cluster":false},"cluster_type":"elasticsearch","auth_info":{"password":"testpassword","username":"elasticsearch"},"storage_config":{"base_index":"test_data_source_table_id","date_format":"%%Y%%m%%d%%H","index_datetime_format":"write_2006010215","index_datetime_timezone":0,"index_settings":{},"mapping_settings":{},"retention":0,"slice_gap":120,"slice_size":500,"warm_phase_days":0,"warm_phase_settings":{}}}]}],"source_label":"bk_monitor","space_type_id":"all","space_uid":"","token":"9e679720296f4ad7abf5ad95ac0acbdf","transfer_cluster_id":"default","type_label":"event"}`, es.StorageClusterID)
	equal, err := jsonx.CompareJson(dsConfigJson, targetJson)
	assert.NoError(t, err)
	assert.True(t, equal)

	ds.EtlConfig = models.ETLConfigTypeBkStandardV2TimeSeries
	err = ds.Update(db)
	assert.NoError(t, err)

	// 自定义上报没有field_list
	ds.EtlConfig = models.ETLConfigTypeBkStandardV2TimeSeries
	err = ds.Update(db)
	assert.NoError(t, err)
	dsConfig, err = dsSvc.ToJson(true, true)
	assert.NoError(t, err)
	dsConfigJson, err = jsonx.MarshalString(dsConfig)
	// 	去除时间字段，避免影响比对
	matchedList2 := re.FindAllStringSubmatch(dsConfigJson, -1)
	for _, s := range matchedList2 {
		dsConfigJson = strings.ReplaceAll(dsConfigJson, s[1], "0")
	}
	targetJson2 := fmt.Sprintf(`{"bk_data_id":99999,"data_id":99999,"data_name":"test_data_source","etl_config":"bk_standard_v2_time_series","is_platform_data_id":false,"mq_config":{"auth_info":{"password":"","username":""},"batch_size":null,"cluster_config":{"domain_name":"127.0.0.1","port":9096,"extranet_domain_name":"","extranet_port":0,"schema":null,"is_ssl_verify":false,"ssl_verification_mode":"","ssl_insecure_skip_verify":false,"ssl_certificate_authorities":"","ssl_certificate":"","ssl_certificate_key":"","raw_ssl_certificate_authorities":"","raw_ssl_certificate":"","raw_ssl_certificate_key":"","cluster_id":1007,"cluster_name":"kafka_default_test","version":null,"custom_option":"","registered_system":"","creator":"","create_time":0,"last_modify_user":"","is_default_cluster":true},"cluster_type":"kafka","consume_rate":null,"flush_interval":null,"storage_config":{"partition":1,"topic":"0bkmonitor_999990"}},"option":{"test_bool":true,"test_string":"string abc"},"result_table_list":[{"bk_biz_id":0,"field_list":[],"option":{},"result_table":"test_data_source_table_id","schema_type":"","shipper_list":[{"cluster_config":{"domain_name":"127.0.0.1","port":9200,"extranet_domain_name":"","extranet_port":0,"schema":"http","is_ssl_verify":false,"ssl_verification_mode":"","ssl_insecure_skip_verify":false,"ssl_certificate_authorities":"","ssl_certificate":"","ssl_certificate_key":"","raw_ssl_certificate_authorities":"","raw_ssl_certificate":"","raw_ssl_certificate_key":"","cluster_id":%v,"cluster_name":"test_es_0002","version":"7","custom_option":"","registered_system":"","creator":"","create_time":0,"last_modify_user":"","is_default_cluster":false},"cluster_type":"elasticsearch","auth_info":{"password":"testpassword","username":"elasticsearch"},"storage_config":{"base_index":"test_data_source_table_id","date_format":"%%Y%%m%%d%%H","index_datetime_format":"write_2006010215","index_datetime_timezone":0,"index_settings":{},"mapping_settings":{},"retention":0,"slice_gap":120,"slice_size":500,"warm_phase_days":0,"warm_phase_settings":{}}}]}],"source_label":"bk_monitor","space_type_id":"all","space_uid":"","token":"9e679720296f4ad7abf5ad95ac0acbdf","transfer_cluster_id":"default","type_label":"event"}`, es.StorageClusterID)
	equal, err = jsonx.CompareJson(dsConfigJson, targetJson2)
	assert.NoError(t, err)
	assert.True(t, equal)
}

func TestDataSourceSvc_AddBuiltInChannelIdToGse(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"message":"ok","result":true,"code":0,"data":{}}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	cluster := storage.ClusterInfo{
		ClusterName:   "kafka_test_cluster",
		ClusterType:   models.StorageTypeKafka,
		DomainName:    "127.0.0.1",
		Port:          9096,
		GseStreamToId: 0,
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err := cluster.Create(db)
	assert.NoError(t, err)
	topic := storage.KafkaTopicInfo{
		BkDataId:  1199999,
		Topic:     "test_kafka_topic_1",
		Partition: 1,
	}
	db.Delete(&topic)
	err = topic.Create(db)
	assert.NoError(t, err)
	ds := resulttable.DataSource{BkDataId: 1199999, MqClusterId: cluster.ClusterID}
	err = NewDataSourceSvc(&ds).AddBuiltInChannelIdToGse()
	assert.NoError(t, err)
}

func TestDataSourceSvc_StorageConsulConfig(t *testing.T) {
	s := StorageConsulConfig{
		ClusterInfoConsulConfig: ClusterInfoConsulConfig{
			ClusterConfig: ClusterConfig{
				DomainName:                   "",
				Port:                         0,
				ExtranetDomainName:           "",
				ExtranetPort:                 0,
				IsSslVerify:                  false,
				SslVerificationMode:          "",
				SslInsecureSkipVerify:        false,
				SslCertificateAuthorities:    "",
				SslCertificate:               "",
				SslCertificateKey:            "",
				RawSslCertificateAuthorities: "",
				RawSslCertificate:            "",
				RawSslCertificateKey:         "",
				ClusterId:                    0,
				ClusterName:                  "",
				CustomOption:                 "",
				RegisteredSystem:             "",
				Creator:                      "",
				CreateTime:                   0,
				LastModifyUser:               "",
				IsDefaultCluster:             false,
			},
			ClusterType: "",
			AuthInfo:    AuthInfo{},
		},
		StorageConfig: nil,
	}
	str, _ := jsonx.MarshalString(s)
	fmt.Println(str)
}

func TestDataSourceConsulPath(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	var bkDataId uint = 1000001
	ds := &resulttable.DataSource{
		BkTenantId:        "system",
		BkDataId:          bkDataId,
		Token:             "9e679720296f4ad7abf5ad95ac0acbdf",
		DataName:          "test_data_source",
		DataDescription:   "data source for test",
		MqClusterId:       1,
		MqConfigId:        21,
		EtlConfig:         "bk_standard_v2_event",
		IsCustomSource:    true,
		Creator:           "admin",
		CreateTime:        time.Time{},
		LastModifyUser:    "admin",
		LastModifyTime:    time.Time{},
		TypeLabel:         "event",
		SourceLabel:       "bk_monitor",
		IsEnable:          true,
		TransferClusterId: "default",
		SpaceTypeId:       "all",
		SpaceUid:          "",
		CreatedFrom:       "bkgse",
	}
	err := db.Delete(&ds).Error
	assert.NoError(t, err)
	err = db.Create(&ds).Error
	assert.NoError(t, err)

	var dsObj resulttable.DataSource
	err = resulttable.NewDataSourceQuerySet(db).BkDataIdEq(bkDataId).One(&dsObj)
	assert.NoError(t, err)

	dsSvc := NewDataSourceSvc(&dsObj)
	consulPath := dsSvc.ConsulConfigPath()
	assert.Contains(t, consulPath, strconv.Itoa(int(bkDataId)))
}
