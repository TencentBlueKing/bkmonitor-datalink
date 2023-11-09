// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// EventType :
type EventType int

// ResultTableSchemaType :
type ResultTableSchemaType string

type MetadataConfig interface {
	Clean() error
}

// MetaClusterInfo :
type MetaClusterInfo struct {
	ClusterConfig   map[string]interface{} `mapstructure:"cluster_config" json:"cluster_config"`
	StorageConfig   map[string]interface{} `mapstructure:"storage_config" json:"storage_config"`
	AuthInfo        map[string]interface{} `mapstructure:"auth_info" json:"auth_info"`
	ClusterType     string                 `mapstructure:"cluster_type" json:"cluster_type"`
	BatchSize       int                    `mapstructure:"batch_size" json:"batch_size"`
	BulkConcurrency int64                  `mapstructure:"bulk_concurrency" json:"bulk_concurrency"`
	FlushInterval   string                 `mapstructure:"flush_interval" json:"flush_interval"`
	ConsumeRate     int                    `mapstructure:"consume_rate" json:"consume_rate"` // unit:Bytes
}

// NewMetaClusterInfo :
func NewMetaClusterInfo() *MetaClusterInfo {
	return &MetaClusterInfo{
		ClusterConfig: make(map[string]interface{}),
		StorageConfig: make(map[string]interface{}),
		AuthInfo:      make(map[string]interface{}),
	}
}

// Clean:
func (c *MetaClusterInfo) Clean() error {
	if c.ClusterConfig == nil {
		c.ClusterConfig = map[string]interface{}{}
	}
	if c.StorageConfig == nil {
		c.StorageConfig = map[string]interface{}{}
	}
	if c.AuthInfo == nil {
		c.AuthInfo = map[string]interface{}{}
	}
	return nil
}

// MustGetStorageConfig :
func (c *MetaClusterInfo) MustGetStorageConfig(key string) interface{} {
	value, ok := c.StorageConfig[key]
	if !ok {
		panic(errors.WithMessage(define.ErrKey, key))
	}
	return value
}

// MustGetClusterConfig :
func (c *MetaClusterInfo) MustGetClusterConfig(key string) interface{} {
	value, ok := c.ClusterConfig[key]
	if !ok {
		panic(errors.WithMessage(define.ErrKey, key))
	}
	return value
}

// MustGetClusterConfig :
func (c *MetaClusterInfo) MustGetAuthInfo(key string) interface{} {
	value, ok := c.AuthInfo[key]
	if !ok {
		panic(errors.WithMessage(define.ErrKey, key))
	}
	return value
}

// MetaFieldConfig :
type MetaFieldConfig struct {
	Option         map[string]interface{}  `mapstructure:"option" json:"option"`
	Type           define.MetaFieldType    `mapstructure:"type" json:"type"`
	IsConfigByUser bool                    `mapstructure:"is_config_by_user" json:"is_config_by_user"`
	Tag            define.MetaFieldTagType `mapstructure:"tag" json:"tag"`
	FieldName      string                  `mapstructure:"field_name" json:"field_name"`
	AliasName      string                  `mapstructure:"alias_name" json:"alias_name"`
	DefaultValue   interface{}             `mapstructure:"default_value" json:"default_value"`
	Disabled       bool                    `mapstructure:"is_disabled" json:"is_disabled"`
}

func (c *MetaFieldConfig) Clean() error {
	c.Option = utils.NewMapHelper(c.Option).Data
	return nil
}

// HasDefaultValue
func (c *MetaFieldConfig) HasDefaultValue() bool {
	return c.DefaultValue == nil
}

// Name
func (c *MetaFieldConfig) Name() string {
	if c.AliasName != "" {
		return c.AliasName
	}
	return c.FieldName
}

// Path
func (c *MetaFieldConfig) Path() string {
	option := utils.NewMapHelper(c.Option)
	path, ok := option.GetString(MetaFieldOptRealPath)
	if ok {
		return path
	}

	return c.FieldName
}

// MetaResultTableConfig :
type MetaResultTableConfig struct {
	Option      map[string]interface{} `mapstructure:"option" json:"option"`
	SchemaType  ResultTableSchemaType  `mapstructure:"schema_type" json:"schema_type"`
	ShipperList []*MetaClusterInfo     `mapstructure:"shipper_list" json:"shipper_list"`
	ResultTable string                 `mapstructure:"result_table" json:"result_table"`
	FieldList   []*MetaFieldConfig     `mapstructure:"field_list" json:"field_list"`
	MultiNum    int                    `mapstructure:"multi_num" json:"multi_num"`
}

// MappingResultTable 映射 resultTable 名称 可在数据复制功能中使用到
func (c *MetaResultTableConfig) MappingResultTable() string {
	obj, ok := c.Option["mapping_result_table"]
	if !ok {
		return c.ResultTable
	}

	s, ok := obj.(string)
	if !ok || s == "" {
		return c.ResultTable
	}
	return s
}

func (c *MetaResultTableConfig) DisabledBizID() map[string]struct{} {
	ids := make(map[string]struct{})
	disabled, ok := c.Option["disabled_bizid"]
	if !ok {
		return ids
	}
	s, ok := disabled.(string)
	if !ok {
		return ids
	}

	for _, part := range strings.Split(s, ",") {
		bizID := strings.TrimSpace(part)
		if len(bizID) > 0 {
			ids[bizID] = struct{}{}
		}
	}
	return ids
}

func (c *MetaResultTableConfig) Clean() error {
	var err error
	for _, value := range c.ShipperList {
		if err := value.Clean(); err != nil {
			return err
		}
	}
	for _, value := range c.FieldList {
		if err := value.Clean(); err != nil {
			return err
		}
	}
	c.Option = utils.NewMapHelper(c.Option).Data
	// 默认值为1
	if c.MultiNum == 0 {
		c.MultiNum = 1
	}
	return err
}

// FormatName :
func (c *MetaResultTableConfig) FormatName(name string) string {
	return fmt.Sprintf("%s/%s", name, c.ResultTable)
}

// FieldListGroupByName :
func (c *MetaResultTableConfig) FieldListGroupByName() map[string]*MetaFieldConfig {
	mappings := make(map[string]*MetaFieldConfig)
	for _, f := range c.FieldList {
		mappings[f.FieldName] = f
	}
	return mappings
}

// MetaResultTableConfigFieldVisitFunc :
type MetaResultTableConfigFieldVisitFunc func(config *MetaFieldConfig) error

// VisitUserSpecifiedFields
func (c *MetaResultTableConfig) VisitUserSpecifiedFields(fn MetaResultTableConfigFieldVisitFunc) error {
	for _, f := range c.FieldList {
		err := fn(f)
		if err != nil {
			return err
		}
	}
	return nil
}

// VisitFieldByTag :
func (c *MetaResultTableConfig) VisitFieldByTag(metricFn MetaResultTableConfigFieldVisitFunc, dimensionFn MetaResultTableConfigFieldVisitFunc) error {
	for _, f := range c.FieldList {
		switch f.Tag {
		case define.MetaFieldTagMetric:
			if metricFn == nil {
				continue
			}
			err := metricFn(f)
			if err != nil {
				return err
			}
		case define.MetaFieldTagDimension:
			if dimensionFn == nil {
				continue
			}
			err := dimensionFn(f)
			if err != nil {
				return err
			}
		case define.MetaFieldTagGroup:
			if dimensionFn == nil {
				continue
			}
			err := dimensionFn(f)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ShipperListGroupByType :
func (c *MetaResultTableConfig) ShipperListGroupByType() map[string][]*MetaClusterInfo {
	mappings := make(map[string][]*MetaClusterInfo)
	for _, s := range c.ShipperList {
		list, ok := mappings[s.ClusterType]
		if !ok {
			list = make([]*MetaClusterInfo, 0)
		}
		mappings[s.ClusterType] = append(list, s)
	}
	return mappings
}

// PipelineConfig :
type PipelineConfig struct {
	Option          map[string]interface{}   `mapstructure:"option" json:"option"`
	ETLConfig       string                   `mapstructure:"etl_config" json:"etl_config"`
	ResultTableList []*MetaResultTableConfig `mapstructure:"result_table_list" json:"result_table_list"`
	MQConfig        *MetaClusterInfo         `mapstructure:"mq_config" json:"mq_config"`
	DataID          int                      `mapstructure:"data_id" json:"data_id"`
	TypeLabel       string                   `mapstructure:"type_label" json:"type_label"`
}

// Clean :
func (c *PipelineConfig) Clean() error {
	var err error
	if c.MQConfig == nil {
		c.MQConfig = NewMetaClusterInfo()
	} else {
		if err := c.MQConfig.Clean(); err != nil {
			return err
		}
	}
	for _, value := range c.ResultTableList {
		if err = value.Clean(); err != nil {
			return err
		}
	}
	c.Option = utils.NewMapHelper(c.Option).Data // 这里可以用执行模板处理

	return err
}

// FormatName :
func (c *PipelineConfig) FormatName(name string) string {
	return fmt.Sprintf("%s:%d", name, c.DataID)
}

// NewPipelineConfig :
func NewPipelineConfig() *PipelineConfig {
	return &PipelineConfig{
		ResultTableList: make([]*MetaResultTableConfig, 0),
		MQConfig:        NewMetaClusterInfo(),
		Option:          make(map[string]interface{}),
	}
}

// ShipperConfigFromContext :
func ShipperConfigFromContext(ctx context.Context) *MetaClusterInfo {
	v := ctx.Value(define.ContextShipperKey)
	switch info := v.(type) {
	case *MetaClusterInfo:
		return info
	case MetaClusterInfo:
		return &info
	default:
		return nil
	}
}

// ShipperConfigIntoContext :
func ShipperConfigIntoContext(ctx context.Context, shipper *MetaClusterInfo) context.Context {
	return context.WithValue(ctx, define.ContextShipperKey, shipper)
}

// MQConfigFromContext :
func MQConfigFromContext(ctx context.Context) *MetaClusterInfo {
	v := ctx.Value(define.ContextMQConfigKey)
	switch info := v.(type) {
	case *MetaClusterInfo:
		return info
	case MetaClusterInfo:
		return &info
	default:
		return nil
	}
}

// MQConfigIntoContext :
func MQConfigIntoContext(ctx context.Context, mqConfig *MetaClusterInfo) context.Context {
	return context.WithValue(ctx, define.ContextMQConfigKey, mqConfig)
}

// PipelineConfigFromContext :
func PipelineConfigFromContext(ctx context.Context) *PipelineConfig {
	v := ctx.Value(define.ContextPipelineKey)
	switch info := v.(type) {
	case *PipelineConfig:
		return info
	case PipelineConfig:
		return &info
	default:
		return nil
	}
}

// PipelineConfigIntoContext :
func PipelineConfigIntoContext(ctx context.Context, pipeline *PipelineConfig) context.Context {
	return context.WithValue(ctx, define.ContextPipelineKey, pipeline)
}

// ResultTableConfigFromContext :
func ResultTableConfigFromContext(ctx context.Context) *MetaResultTableConfig {
	v := ctx.Value(define.ContextResultTableKey)
	switch info := v.(type) {
	case *MetaResultTableConfig:
		return info
	case MetaResultTableConfig:
		return &info
	default:
		return nil
	}
}

// ResultTableConfigIntoContext :
func ResultTableConfigIntoContext(ctx context.Context, table *MetaResultTableConfig) context.Context {
	return context.WithValue(ctx, define.ContextResultTableKey, table)
}

// RuntimeConfig 运行时产生的参数可以写在这里
type RuntimeConfig struct {
	PipelineCount int `mapstructure:"pipeline_count" json:"pipeline_count"`
}

func RuntimeConfigIntoContext(ctx context.Context, runtime *RuntimeConfig) context.Context {
	return context.WithValue(ctx, define.ContextRuntimeKey, runtime)
}

func RuntimeConfigFromContext(ctx context.Context) *RuntimeConfig {
	v := ctx.Value(define.ContextRuntimeKey)
	switch info := v.(type) {
	case *RuntimeConfig:
		return info
	case RuntimeConfig:
		return &info
	default:
		return nil
	}
}
