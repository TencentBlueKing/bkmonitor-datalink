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
	"slices"
	"time"

	"github.com/spf13/viper"
)

const FTAEvent = "bk_fta_event"

var FTAClean struct {
	Enabled        bool
	All            bool
	DataIDs        []int
	Shards         int
	MaxConcurrency int
	BatchMessages  int
	BatchBytes     int
	Refresh        time.Duration
	BatchWait      time.Duration
	GroupPrefix    string
}

func loadFTAClean() {
	FTAClean.Enabled = GetValue("fta_clean.enabled", false)
	FTAClean.All = GetValue("fta_clean.all", false)
	FTAClean.DataIDs = GetValue("fta_clean.data_ids", []int{}, viper.GetIntSlice)
	FTAClean.Shards = GetValue("fta_clean.shards", 4)
	FTAClean.MaxConcurrency = GetValue("fta_clean.max_concurrency", 8)
	FTAClean.BatchMessages = GetValue("fta_clean.batch_messages", 128)
	FTAClean.BatchBytes = GetValue("fta_clean.batch_bytes", 1<<20)
	FTAClean.Refresh = GetValue("fta_clean.refresh_interval", time.Minute, viper.GetDuration)
	FTAClean.BatchWait = GetValue("fta_clean.batch_wait", 50*time.Millisecond, viper.GetDuration)
	// Match the shipped transfer.yaml and metadata TRANSFER_CONSUMER_GROUP_ID.
	FTAClean.GroupPrefix = GetValue("fta_clean.consumer_group_prefix", "bkmonitorv3_transfer")
}

func FTACleanOwns(etl string, id uint) bool {
	return FTAClean.Enabled && etl == FTAEvent && (FTAClean.All || slices.Contains(FTAClean.DataIDs, int(id)))
}
