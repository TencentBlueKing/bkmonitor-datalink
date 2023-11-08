// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

//go:generate goqueryset -in bcslabel.go -out qs_bcslabel_gen.go

// BCSLabel BCS label model
// gen:qs
type BCSLabel struct {
	HashID uint   `gorm:"primary_key;column:hash_id" json:"hash_id"`
	Key    string `gorm:"size:127;" json:"key"`
	Value  string `gorm:"size:127;" json:"value"`
}

// TableName 用于设置表的别名
func (BCSLabel) TableName() string {
	return "bkmonitor_bcslabel"
}
