// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"context"
	"reflect"
	"strconv"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type OptionBase struct {
	ValueType  string    `json:"value_type" gorm:"size:64"`
	Value      string    `json:"value" gorm:"value"`
	Creator    string    `json:"creator" gorm:"size:32"`
	CreateTime time.Time `json:"create_time"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (r *OptionBase) BeforeCreate(tx *gorm.DB) error {
	r.CreateTime = time.Now()
	return nil
}

type BaseModel struct {
	Creator    string    `gorm:"column:creator" json:"creator"`
	CreateTime time.Time `gorm:"column:create_time" json:"create_time"`
	Updater    string    `gorm:"column:updater" json:"updater"`
	UpdateTime time.Time `gorm:"column:update_time" json:"update_time"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (b *BaseModel) BeforeCreate(tx *gorm.DB) error {
	b.CreateTime = time.Now()
	b.UpdateTime = time.Now()
	if b.Creator == "" {
		b.Creator = SystemUser
	}
	if b.Updater == "" {
		b.Updater = SystemUser
	}
	return nil
}

// BeforeUpdate 保存前最后修改时间字段设置为当前时间
func (b *BaseModel) BeforeUpdate(tx *gorm.DB) error {
	b.UpdateTime = time.Now()
	return nil
}

// InterfaceValue 将字符串转为interface{}类型
func (r *OptionBase) InterfaceValue() (any, error) {
	var value any
	switch r.ValueType {
	case "string":
		value = r.Value
		return value, nil
	case "bool":
		value = r.Value == "true"
		return value, nil
	default:
		err := jsonx.UnmarshalString(r.Value, &value)
		if err != nil {
			return nil, err
		}
		return value, nil
	}
}

// ParseOptionValue 解析option的interface{}的类型
func ParseOptionValue(value any) (string, string, error) {
	if value == nil {
		return "", "", errors.New("ParseOptionValue value can not be nil")
	}
	valueStr, err := jsonx.MarshalString(value)
	if err != nil {
		return "", "", err
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Bool:
		return valueStr, "bool", nil
	case reflect.Slice, reflect.Array:
		return valueStr, "list", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return valueStr, "int", nil
	case reflect.Map:
		return valueStr, "dict", nil
	case reflect.String:
		valueStr, ok := value.(string)
		if !ok {
			return "", "", errors.Errorf("assert string value type error, %#v", value)
		}
		return valueStr, "string", nil
	default:
		return "", "", errors.Errorf("unsupport option value type [%s], value [%v]", reflect.TypeOf(value).Kind().String(), value)
	}
}

// RefreshRouterVersion 更新consul中的version
func RefreshRouterVersion(ctx context.Context, path string) error {
	client, err := consul.GetInstance()
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	err = client.Put(path, timestamp, 0, 0)
	if err != nil {
		return err
	}
	logger.Infof("update %s version [%s] success", path, timestamp)
	return nil
}

// IsBuildInDataId 检查是否为内置data_id
func IsBuildInDataId(bkDataId uint) bool {
	return (1000 <= bkDataId && bkDataId <= 1020) || (1100000 <= bkDataId && bkDataId <= 1199999)
}

type BaseModelWithTime struct {
	Creator  string    `gorm:"column:creator" json:"creator"`
	CreateAt time.Time `gorm:"column:create_at" json:"create_at"`
	Updater  string    `gorm:"column:updater" json:"updater"`
	UpdateAt time.Time `gorm:"column:update_at" json:"update_at"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (b *BaseModelWithTime) BeforeCreate(tx *gorm.DB) error {
	b.CreateAt = time.Now()
	b.UpdateAt = time.Now()
	if b.Creator == "" {
		b.Creator = SystemUser
	}
	if b.Updater == "" {
		b.Updater = SystemUser
	}
	return nil
}

// BeforeUpdate 保存前最后修改时间字段设置为当前时间
func (b *BaseModelWithTime) BeforeUpdate(tx *gorm.DB) error {
	b.UpdateAt = time.Now()
	return nil
}
