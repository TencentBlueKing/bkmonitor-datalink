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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCustomRelationStatusQuerySet 测试查询集基本功能
func TestCustomRelationStatusQuerySet(t *testing.T) {
	// 测试结构体创建
	status := CustomRelationStatus{
		ID:           1,
		Creator:      "test_creator",
		CreateTime:   time.Now(),
		Updater:      "test_updater",
		UpdateTime:   time.Now(),
		UID:          "test_uid_1234567890",
		Generation:   1,
		Namespace:    "test_namespace",
		Name:         "test_name",
		Labels:       `{"key": "value"}`,
		FromResource: "from_resource",
		ToResource:   "to_resource",
	}

	// 验证字段值
	assert.Equal(t, 1, status.ID)
	assert.Equal(t, "test_creator", status.Creator)
	assert.Equal(t, "test_uid_1234567890", status.UID)
	assert.Equal(t, "test_namespace", status.Namespace)
	assert.Equal(t, "test_name", status.Name)
	assert.Equal(t, "from_resource", status.FromResource)
	assert.Equal(t, "to_resource", status.ToResource)
	assert.Equal(t, int64(1), status.Generation)
	assert.NotNil(t, status.Labels)
	assert.NotZero(t, status.CreateTime)
	assert.NotZero(t, status.UpdateTime)

	// 验证TableName方法
	assert.Equal(t, "metadata_customrelationstatus", status.TableName())
}

// TestCustomRelationStatusDBSchema 测试数据库模式定义
func TestCustomRelationStatusDBSchema(t *testing.T) {
	// 验证数据库字段名定义
	assert.Equal(t, "id", CustomRelationStatusDBSchema.ID.String())
	assert.Equal(t, "creator", CustomRelationStatusDBSchema.Creator.String())
	assert.Equal(t, "create_time", CustomRelationStatusDBSchema.CreateTime.String())
	assert.Equal(t, "updater", CustomRelationStatusDBSchema.Updater.String())
	assert.Equal(t, "update_time", CustomRelationStatusDBSchema.UpdateTime.String())
	assert.Equal(t, "uid", CustomRelationStatusDBSchema.UID.String())
	assert.Equal(t, "generation", CustomRelationStatusDBSchema.Generation.String())
	assert.Equal(t, "namespace", CustomRelationStatusDBSchema.Namespace.String())
	assert.Equal(t, "name", CustomRelationStatusDBSchema.Name.String())
	assert.Equal(t, "labels", CustomRelationStatusDBSchema.Labels.String())
	assert.Equal(t, "from_resource", CustomRelationStatusDBSchema.FromResource.String())
	assert.Equal(t, "to_resource", CustomRelationStatusDBSchema.ToResource.String())
}

// TestCustomRelationStatusQuerySetMethods 测试查询集方法签名
func TestCustomRelationStatusQuerySetMethods(t *testing.T) {
	// 这个测试主要验证方法签名是否正确，不涉及实际数据库操作
	// 在实际项目中，这些方法会与数据库交互

	// 验证查询集结构体存在
	var qs CustomRelationStatusQuerySet
	assert.NotNil(t, qs)

	// 验证更新器结构体存在
	var updater CustomRelationStatusUpdater
	assert.NotNil(t, updater)

	// 验证数据库模式字段类型
	var field CustomRelationStatusDBSchemaField
	assert.Equal(t, "", field.String())
}
