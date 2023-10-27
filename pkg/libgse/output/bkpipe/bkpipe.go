// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe

import (
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/outputs"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

func init() {
	outputs.RegisterType("bkpipe", MakeBKPipe)
	outputs.RegisterType("bkpipe_ignore", MakeBKPipeWithoutCheckConn)
}

// MakeBKPipe create gse output
// compatible with old configurations
func MakeBKPipe(im outputs.IndexManager, beat beat.Info, stats outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	return gse.MakeGSE(im, beat, stats, cfg)
}

// MakeBKPipeWithoutCheckConn create gse output without check connection
func MakeBKPipeWithoutCheckConn(im outputs.IndexManager, beat beat.Info, stats outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	return gse.MakeGSEWithoutCheckConn(im, beat, stats, cfg)
}
