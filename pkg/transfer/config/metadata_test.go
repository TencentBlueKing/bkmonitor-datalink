// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ConsulConfigSuite :
type ConsulConfigSuite struct {
	suite.Suite
}

// UnmarshalBy :
func (s *ConsulConfigSuite) UnmarshalBy(data string, v interface{}) {
	s.NoError(json.Unmarshal([]byte(data), v))
}

// PipelineConfigSuite :
type PipelineConfigSuite struct {
	ConsulConfigSuite
}

// TestOption :
func (s *PipelineConfigSuite) TestOption() {
	cases := []struct {
		options            map[string]interface{}
		key                string
		defaults, excepted interface{}
	}{
		{nil, "ok", false, false},
		{map[string]interface{}{}, "ok", false, false},
		{map[string]interface{}{"ok": true}, "ok", false, true},
		{map[string]interface{}{"ok": 1}, "ok", false, 1},
	}

	for _, c := range cases {
		pipe := config.PipelineConfig{
			Option: c.options,
		}
		s.Equal(c.excepted, utils.NewMapHelper(pipe.Option).GetOrDefault(c.key, c.defaults))
	}
}

// TestStruct :
func (s *PipelineConfigSuite) TestStruct() {
	consul := `
{
  "etl_config": "snapshot",
  "result_table_list": [
    {
      "option": {"disabled_bizid": "137, 136, ,"},
      "schema_type": "free",
      "shipper_list": [
        {
          "cluster_config": {
            "domain_name": "influxdb.service.consul",
            "port": 8086
          },
          "storage_config": {
            "real_table_name": "table_v10",
            "database": "system"
          },
          "cluster_type": "influxdb"
        }
      ],
      "result_table": "system.cpu_detail",
      "field_list": [
        {
          "type": "int",
          "is_config_by_user": true,
          "tag": "",
          "field_name": "bk_biz_id"
        },
        {
          "type": "int",
          "is_config_by_user": true,
          "tag": "dimension",
          "field_name": "custom_field"
        },
        {
          "type": "timestamp",
          "is_config_by_user": true,
          "tag": "",
          "field_name": "local_time"
        },
        {
          "type": "timestamp",
          "is_config_by_user": true,
          "tag": "",
          "field_name": "time"
        }
      ]
    }
  ],
  "mq_config": {
    "cluster_config": {
      "domain_name": "kafka.service.consul",
      "port": 9092
    },
    "storage_config": {
      "topic": "bkmonitor_130",
      "partition": 1	
    },
    "auth_info": {
      "username": "admin",
      "password": "admin"
    },
    "cluster_type": "kafka"
  },
  "data_id": 13
}
`
	var pipe config.PipelineConfig
	s.UnmarshalBy(consul, &pipe)

	cases := []struct {
		value interface{}
		ref   interface{}
	}{
		{13, pipe.DataID},
		{"snapshot", pipe.ETLConfig},

		{"kafka", pipe.MQConfig.ClusterType},
		{"kafka.service.consul", pipe.MQConfig.ClusterConfig["domain_name"]},
		{9092.0, pipe.MQConfig.ClusterConfig["port"]},
		{"bkmonitor_130", pipe.MQConfig.StorageConfig["topic"]},
		{1.0, pipe.MQConfig.StorageConfig["partition"]},
		{"admin", pipe.MQConfig.AuthInfo["username"]},
		{"admin", pipe.MQConfig.AuthInfo["password"]},
		{config.ResultTableSchemaTypeFree, pipe.ResultTableList[0].SchemaType},
		{"system.cpu_detail", pipe.ResultTableList[0].ResultTable},

		{"influxdb", pipe.ResultTableList[0].ShipperList[0].ClusterType},
		{"influxdb.service.consul", pipe.ResultTableList[0].ShipperList[0].ClusterConfig["domain_name"]},
		{8086.0, pipe.ResultTableList[0].ShipperList[0].ClusterConfig["port"]},
		{"system", pipe.ResultTableList[0].ShipperList[0].StorageConfig["database"]},
		{"table_v10", pipe.ResultTableList[0].ShipperList[0].StorageConfig["real_table_name"]},

		{define.MetaFieldType("int"), pipe.ResultTableList[0].FieldList[0].Type},
		{true, pipe.ResultTableList[0].FieldList[0].IsConfigByUser},
		{define.MetaFieldTagType(""), pipe.ResultTableList[0].FieldList[0].Tag},
		{"bk_biz_id", pipe.ResultTableList[0].FieldList[0].FieldName},

		{define.MetaFieldType("int"), pipe.ResultTableList[0].FieldList[1].Type},
		{true, pipe.ResultTableList[0].FieldList[1].IsConfigByUser},
		{define.MetaFieldTagType("dimension"), pipe.ResultTableList[0].FieldList[1].Tag},
		{"custom_field", pipe.ResultTableList[0].FieldList[1].FieldName},

		{define.MetaFieldType("timestamp"), pipe.ResultTableList[0].FieldList[2].Type},
		{true, pipe.ResultTableList[0].FieldList[2].IsConfigByUser},
		{define.MetaFieldTagType(""), pipe.ResultTableList[0].FieldList[2].Tag},
		{"local_time", pipe.ResultTableList[0].FieldList[2].FieldName},

		{define.MetaFieldType("timestamp"), pipe.ResultTableList[0].FieldList[3].Type},
		{true, pipe.ResultTableList[0].FieldList[3].IsConfigByUser},
		{define.MetaFieldTagType(""), pipe.ResultTableList[0].FieldList[3].Tag},
		{"time", pipe.ResultTableList[0].FieldList[3].FieldName},
	}
	for i, c := range cases {
		s.Equalf(c.value, c.ref, "index %d", i)
	}
	s.Equal(pipe.ResultTableList[0].DisabledBizID(), map[string]struct{}{"137": {}, "136": {}})
}

func (s *PipelineConfigSuite) TestPipelineConfig_Clean() {
	consul := `
{
  "result_table_list": [
    {
      "option": {
        "es_unique_field_list": []
      },
      "schema_type": "free",
      "result_table": "2_log.log",
      "field_list": []
    }
  ],
  "source_label": "bk_monitor",
  "type_label": "log",
  "data_id": 1200151,
  "etl_config": "bk_log_text",
  "option": {"k":"v"}
}
`
	var l1, l2 []*config.MetaClusterInfo
	s.Equal(l1, l2)

	var pipe, pipeClean config.PipelineConfig
	s.UnmarshalBy(consul, &pipe)
	s.UnmarshalBy(consul, &pipeClean)
	s.NoError(pipeClean.Clean())
	s.Equal(pipe.Option, pipeClean.Option)
	s.Equal(pipe.ETLConfig, pipeClean.ETLConfig)
	s.Equal(pipe.DataID, pipeClean.DataID)
}

func (s *PipelineConfigSuite) TestPipelineConfigOptionClean() {
	consul := `{"etl_config":"snapshot","result_table_list":[{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":8086},"storage_config":{"real_table_name":"table_v10","database":"system"},"cluster_type":"influxdb"}],"result_table":"system.cpu_detail","field_list":[{"type":"int","is_config_by_user":true,"tag":"","field_name":"bk_biz_id"},{"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"custom_field"},{"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"local_time"},{"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"bkmonitor_130","partition":1},"auth_info":{"username":"admin","password":"admin"},"cluster_type":"kafka"},"data_id":13,"option":{"k":"v"}}`
	var pipelineConfig config.PipelineConfig
	s.UnmarshalBy(consul, &pipelineConfig)
	cases := []struct {
		pipeline, clean config.PipelineConfig
	}{
		// 部分填充
		{config.PipelineConfig{
			ETLConfig: "", ResultTableList: nil, MQConfig: nil,
		}, config.PipelineConfig{
			ETLConfig: "", ResultTableList: nil, MQConfig: config.NewMetaClusterInfo(), Option: map[string]interface{}{},
		}},
		// 全不填充
		{
			pipelineConfig, pipelineConfig,
		},
		// 全体填充
		{config.PipelineConfig{}, config.PipelineConfig{
			ResultTableList: nil, MQConfig: config.NewMetaClusterInfo(), Option: map[string]interface{}{},
		}},
	}

	for _, value := range cases {
		pipe := value.pipeline
		s.NoError(pipe.Clean())
		s.Equal(pipe.ETLConfig, value.clean.ETLConfig)
		s.Equal(pipe.Option, value.clean.Option)
		s.Equal(pipe.ResultTableList, value.clean.ResultTableList)
		s.Equal(pipe.MQConfig, value.clean.MQConfig)
		s.Equal(pipe.DataID, value.clean.DataID)
	}
	// 向下填充 rt + MQ
	a := config.PipelineConfig{
		ETLConfig: "", ResultTableList: []*config.MetaResultTableConfig{{Option: nil}}, MQConfig: nil,
	}
	b := config.PipelineConfig{
		ETLConfig: "", ResultTableList: []*config.MetaResultTableConfig{
			{
				Option:      map[string]interface{}{},
				ShipperList: nil,
				FieldList:   nil,
				MultiNum:    1,
			},
		}, MQConfig: config.NewMetaClusterInfo(), Option: map[string]interface{}{},
	}
	utils.CheckError(a.Clean())
	s.Equal(a.ResultTableList[0], b.ResultTableList[0])
	s.Equal(a.MQConfig, b.MQConfig)

	// 向下填充 填充顺序pipe -> rt -> shipperList -> NewMetaClusterInfo
	a = config.PipelineConfig{
		ETLConfig: "",
		ResultTableList: []*config.MetaResultTableConfig{{ShipperList: []*config.MetaClusterInfo{
			{ClusterConfig: map[string]interface{}{}, StorageConfig: make(map[string]interface{})},
		}}},
		MQConfig: nil,
	}
	b = config.PipelineConfig{
		ETLConfig: "", ResultTableList: []*config.MetaResultTableConfig{
			{
				Option: map[string]interface{}{},
				ShipperList: []*config.MetaClusterInfo{
					config.NewMetaClusterInfo(),
				},
				FieldList: nil,
				MultiNum:  1,
			},
		}, MQConfig: config.NewMetaClusterInfo(), Option: map[string]interface{}{},
	}
	utils.CheckError(a.Clean())
	s.Equal(a.ResultTableList[0], b.ResultTableList[0])
	s.Equal(a.MQConfig, b.MQConfig)

	// 向下填充 not equal 填充顺序pipe -> rt -> shipperList -> NewMetaClusterInfo
	a = config.PipelineConfig{
		ETLConfig: "",
		ResultTableList: []*config.MetaResultTableConfig{{ShipperList: []*config.MetaClusterInfo{
			{ClusterConfig: map[string]interface{}{}, StorageConfig: make(map[string]interface{})},
		}}},
		MQConfig: nil,
	}

	c := &config.PipelineConfig{}
	s.NoError(utils.DeepCopy(&a, c))
	utils.CheckError(a.Clean())
	s.NotEqual(a.ResultTableList[0], c.ResultTableList[0])
}

// TestPipelineConfigSuite :
func TestPipelineConfigSuite(t *testing.T) {
	suite.Run(t, new(PipelineConfigSuite))
}

// MetaResultTableConfigSuite :
type MetaResultTableConfigSuite struct {
	ConsulConfigSuite
}

// TestFieldListGroupByName :
func (s *MetaResultTableConfigSuite) TestFieldListGroupByName() {
	consul := `
{
  "schema_type": "dynamic",
  "shipper_list": [],
  "result_table": "system.cpu_detail",
  "field_list": [
	{
	  "type": "int",
	  "is_config_by_user": true,
	  "tag": "",
	  "field_name": "bk_biz_id"
	},
	{
	  "type": "int",
	  "is_config_by_user": true,
	  "tag": "dimension",
	  "field_name": "custom_field"
	},
	{
	  "type": "timestamp",
	  "is_config_by_user": true,
	  "tag": "",
	  "field_name": "local_time"
	},
	{
	  "type": "timestamp",
	  "is_config_by_user": true,
	  "tag": "",
	  "field_name": "time"
	}
  ]
}
`
	var conf config.MetaResultTableConfig
	s.UnmarshalBy(consul, &conf)
	fields := conf.FieldListGroupByName()
	s.Equal(4, len(fields))
	for key, field := range fields {
		s.Equal(key, field.FieldName)
	}
}

func combineResultTable(consul string) (a, b config.MetaResultTableConfig) {
	var conf, confClean config.MetaResultTableConfig
	utils.CheckError(json.Unmarshal([]byte(consul), &conf))
	utils.CheckError(confClean.Clean())
	utils.CheckError(json.Unmarshal([]byte(consul), &confClean))
	return conf, confClean
}

func (s *MetaResultTableConfigSuite) TestResultTableList_Clean() {
	consul := `
{
  "schema_type": "dynamic",
  "shipper_list": [],
  "result_table": "system.cpu_detail",
  "field_list": [],
  "multi_num": 1
}
`
	rt, clean := combineResultTable(consul)
	rt.Option = map[string]interface{}{}
	s.Equal(rt, clean)
}

// TestShipperListGroupByType :
func (s *MetaResultTableConfigSuite) TestShipperListGroupByType() {
	consul := `
{
  "schema_type": "dynamic",
  "shipper_list": [
    {
      "cluster_config": {
        "domain_name": "influxdb.service.consul",
        "port": 8086
      },
      "storage_config": {
        "real_table_name": "table_v10",
        "database": "system"
      },
      "cluster_type": "influxdb"
    },
    {
      "cluster_config": {
        "domain_name": "kafka.service.consul",
        "port": 9092
      },
      "storage_config": {
        "topic": "bkmonitor_130",
        "partition": 1
      },
      "cluster_type": "kafka"
    },
    {
      "cluster_config": {
        "domain_name": "influxdb.service.consul",
        "port": 8086
      },
      "storage_config": {
        "real_table_name": "table_v11",
        "database": "system"
      },
      "cluster_type": "influxdb"
    }
  ],
  "field_list": []
}
`
	var conf config.MetaResultTableConfig
	s.UnmarshalBy(consul, &conf)
	shipperMaps := conf.ShipperListGroupByType()
	s.Equal(2, len(shipperMaps))
	for key, shippers := range shipperMaps {
		s.True(len(shippers) > 0)
		for _, shipper := range shippers {
			s.Equal(key, shipper.ClusterType)
		}
	}
}

// TestMetaResultTableConfigSuite :
func TestMetaResultTableConfigSuite(t *testing.T) {
	suite.Run(t, new(MetaResultTableConfigSuite))
}
