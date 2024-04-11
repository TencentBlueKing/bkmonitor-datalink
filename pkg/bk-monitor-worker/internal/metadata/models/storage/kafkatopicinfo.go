// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in kafkatopicinfo.go -out qs_kafkatopicinfo_gen.go

// KafkaTopicInfo kafka topic info model
// gen:qs
type KafkaTopicInfo struct {
	Id            uint    `gorm:"primary_key" json:"id"`
	BkDataId      uint    `gorm:"unique;" json:"bk_data_id"`
	Topic         string  `gorm:"size:128" json:"topic"`
	Partition     int     `gorm:"column:partition" json:"partition"`
	BatchSize     *int64  `gorm:"column:batch_size" json:"batch_size"`
	FlushInterval *string `gorm:"column:flush_interval" json:"flush_interval"`
	ConsumeRate   *int64  `gorm:"column:consume_rate" json:"consume_rate"`
}

// TableName 用于设置表的别名
func (KafkaTopicInfo) TableName() string {
	return "metadata_kafkatopicinfo"
}
