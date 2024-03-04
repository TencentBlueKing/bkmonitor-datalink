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

	"diff/utils/jsondiff"
	"diff/utils/jsonx"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

// redis数据类型
const (
	KeyTypeString = "string"
	KeyTypeHash   = "hash"
	KeyTypeList   = "list"
	KeyTypeSet    = "set"
)

type DiffUtil struct {
	KeyType      string
	OriginKey    string
	BypassKey    string
	OriginConfig *redisUtils.Option
	BypassConfig *redisUtils.Option
}

// Diff 对比redis中指定key数据差异
func (d *DiffUtil) Diff() (bool, error) {
	// get client
	originClient, err := GetRDSClient(d.OriginConfig)
	if err != nil {
		rdsConfig, _ := jsonx.MarshalString(d.OriginConfig)
		return false, errors.Wrapf(err, "get origin redis client with config [%s] failed", rdsConfig)
	}
	bypassClient, err := GetRDSClient(d.BypassConfig)
	if err != nil {
		rdsConfig, _ := jsonx.MarshalString(d.BypassConfig)
		return false, errors.Wrapf(err, "get bypass redis client with config [%s] failed", rdsConfig)
	}

	// get data
	originData, err := d.GetData(originClient, d.OriginKey, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "query originKey [%s] data failed", d.OriginKey)

	}
	bypassData, err := d.GetData(bypassClient, d.BypassKey, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "query bypassKey [%s] data failed", d.BypassKey)
	}

	// compare
	equal, err := d.DiffData(originData, bypassData, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "diff %s data [%s] and [%s] failed", d.KeyType, originData, bypassData)
	}
	if !equal {
		logger.Warnf("origin key [%s] %s data [%s]", d.OriginKey, d.KeyType, originData)
		logger.Warnf("bypass key [%s] %s data [%s]", d.BypassKey, d.KeyType, bypassData)
		return equal, nil
	}
	return equal, nil
}

// GetData 从redis中获数据
func (d *DiffUtil) GetData(rds goRedis.UniversalClient, key string, keyType string) (interface{}, error) {
	switch keyType {
	case KeyTypeString:
		return rds.Get(context.TODO(), key).Result()
	case KeyTypeHash:
		return rds.HGetAll(context.TODO(), key).Result()
	case KeyTypeList:
		return rds.LRange(context.TODO(), key, 0, -1).Result()
	case KeyTypeSet:
		return rds.SMembers(context.TODO(), key).Result()
	default:
		return nil, errors.Errorf("unsupport type [%s]", keyType)
	}
}

// DiffData 对比数据差异
func (d *DiffUtil) DiffData(originData interface{}, bypassData interface{}, keyType string) (bool, error) {
	switch keyType {
	case KeyTypeString:
		return d.compareString(originData, bypassData)
	case KeyTypeHash:
		return d.compareHash(originData, bypassData)
	case KeyTypeList, KeyTypeSet:
		return d.compareList(originData, bypassData)
	default:
		return false, errors.Errorf("unsupport type [%s]", keyType)
	}

}

// 对比string类型数据差异
func (d *DiffUtil) compareString(originData interface{}, bypassData interface{}) (bool, error) {
	origin, ok := originData.(string)
	if !ok {
		return false, errors.Errorf("assert origin data [%#v] to string failed", originData)
	}
	bypass, ok := bypassData.(string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to string failed", bypassData)
	}

	if origin == bypass {
		// 字符串相等直接返回true
		return true, nil
	}

	// 若不相等，尝试解析为json对比
	var o, b interface{}
	if err := jsonx.UnmarshalString(origin, &o); err != nil {
		return false, nil
	}
	if err := jsonx.UnmarshalString(bypass, &b); err != nil {
		return false, nil
	}
	return jsondiff.CompareObjects(o, b)
}

// 对比list/set类型数据差异
func (d *DiffUtil) compareList(originData interface{}, bypassData interface{}) (bool, error) {
	originList, ok := originData.([]string)
	if !ok {
		return false, errors.Errorf("assert origin data [%#v] to slice failed", originData)
	}
	bypassList, ok := bypassData.([]string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to slice failed", bypassData)
	}
	if len(originList) != len(bypassList) {
		return false, nil
	}

	equal, err := jsondiff.CompareObjects(originList, bypassList)
	if err != nil {
		return false, errors.Wrapf(err, "compare list/set [%v] and [%v] failed", originList, bypassList)
	}
	if equal {
		return true, nil
	}

	var oList, bList []interface{}
	for _, origin := range originList {
		var o interface{}
		if err := jsonx.UnmarshalString(origin, &o); err != nil {
			return false, nil
		}
		oList = append(oList, o)
	}

	for _, bypass := range bypassList {
		var b interface{}
		if err := jsonx.UnmarshalString(bypass, &b); err != nil {
			return false, nil
		}
		bList = append(bList, b)
	}

	return jsondiff.CompareObjects(oList, bList)
}

// 对比hash类型数据差异
func (d *DiffUtil) compareHash(originData interface{}, bypassData interface{}) (bool, error) {
	originMap, ok := originData.(map[string]string)
	if !ok {
		return false, errors.Errorf("assert origin data [%#v] to map failed", originData)
	}
	bypassMap, ok := bypassData.(map[string]string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to map failed", bypassData)
	}

	equal, err := jsondiff.CompareObjects(originMap, bypassMap)
	if err != nil {
		return false, errors.Wrapf(err, "compare hash [%v] and [%v] failed", originMap, bypassMap)
	}
	if equal {
		return true, nil
	}

	oMap := make(map[string]interface{})
	bMap := make(map[string]interface{})
	for k, v := range originMap {
		var o interface{}
		if err := jsonx.UnmarshalString(v, &o); err != nil {
			return false, nil
		}
		oMap[k] = o
	}

	for k, v := range bypassMap {
		var b interface{}
		if err := jsonx.UnmarshalString(v, &b); err != nil {
			return false, nil
		}
		bMap[k] = b
	}

	return jsondiff.CompareObjects(oMap, bMap)

}
