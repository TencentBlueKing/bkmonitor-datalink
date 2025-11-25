// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"

	redis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const (
	BkExporter               = "bk_exporter"
	BKTraditionalMeasurement = "bk_traditional_measurement"
	BkSplitMeasurement       = "bk_split_measurement"
	BkStandardV2TimeSeries   = "bk_standard_v2_time_series"
)

type Filter map[string]string

type TsDB struct {
	Type            string   `json:"type"`
	TableID         string   `json:"table_id"`
	Field           []string `json:"field"`
	MeasurementType string   `json:"measurement_type,omitempty"`
	BkDataID        string   `json:"bk_data_id,omitempty"`
	Filters         []Filter `json:"filters"`
	SegmentedEnable bool     `json:"segmented_enable,omitempty"`
	DataLabel       string   `json:"data_label,omitempty"`
}

func (z *TsDB) IsSplit() bool {
	return z.MeasurementType == BkSplitMeasurement
}

func (z *TsDB) String() string {
	return fmt.Sprintf(
		"type:%s,dataLabel:%v,tableID:%v,field:%s,"+
			"measurementType:%s,segmentedEnable:%v,bk_data_id:%s,filter:%+v",
		z.Type, z.DataLabel, z.TableID, z.Field,
		z.MeasurementType, z.SegmentedEnable, z.BkDataID, z.Filters,
	)
}

//go:generate msgp -tests=false
type Space map[string]*TsDB

var GetSpaceIDList = func(ctx context.Context) ([]string, error) {
	return SMembers(ctx, globalInstance.serviceName)
}

var GetSpace = func(ctx context.Context, spaceUid string) (Space, error) {
	key := fmt.Sprintf("%s:%s", globalInstance.serviceName, spaceUid)
	res, err := HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}
	space := make(Space, 0)
	for k, v := range res {
		tsDB := &TsDB{}
		if v != "" {
			err = json.Unmarshal([]byte(v), &tsDB)
			if err != nil {
				return nil, err
			}
			tsDB.TableID = k
			space[k] = tsDB
		}
	}
	return space, nil
}

var SubscribeSpace = func(ctx context.Context) <-chan *redis.Message {
	ch, closeFn := Subscribe(ctx, globalInstance.serviceName)
	go func() {
		<-ctx.Done()
		closeFn()
	}()
	return ch
}
