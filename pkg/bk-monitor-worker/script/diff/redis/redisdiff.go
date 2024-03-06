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

	mapset "github.com/deckarep/golang-set/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

// redis数据类型
const (
	KeyTypeString = "string"
	KeyTypeHash   = "hash"
	KeyTypeList   = "list"
	KeyTypeSet    = "set"
)

type DiffUtil struct {
	Config
}

// Diff 对比redis中指定key数据差异
func (d *DiffUtil) Diff() (bool, error) {
	// get client
	srcClient, err := GetRDSClient(d.SrcConfig)
	if err != nil {
		rdsConfig, _ := jsonx.MarshalString(d.SrcConfig)
		return false, errors.Wrapf(err, "get src redis client with config [%s] failed", rdsConfig)
	}
	bypassClient, err := GetRDSClient(d.BypassConfig)
	if err != nil {
		rdsConfig, _ := jsonx.MarshalString(d.BypassConfig)
		return false, errors.Wrapf(err, "get bypass redis client with config [%s] failed", rdsConfig)
	}

	// get data
	srcData, err := d.GetData(srcClient, d.SrcKey, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "query srcKey [%s] data failed", d.SrcKey)

	}
	bypassData, err := d.GetData(bypassClient, d.BypassKey, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "query bypassKey [%s] data failed", d.BypassKey)
	}

	// compare
	equal, err := d.DiffData(srcData, bypassData, d.KeyType)
	if err != nil {
		return false, errors.Wrapf(err, "diff %s data [%s] and [%s] failed", d.KeyType, srcData, bypassData)
	}
	if !equal {
		fmt.Printf("src key [%s] %s data [%s]\n", d.SrcKey, d.KeyType, srcData)
		fmt.Printf("bypass key [%s] %s data [%s]\n", d.BypassKey, d.KeyType, bypassData)
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
func (d *DiffUtil) DiffData(srcData interface{}, bypassData interface{}, keyType string) (bool, error) {
	switch keyType {
	case KeyTypeString:
		return d.compareString(srcData, bypassData)
	case KeyTypeHash:
		return d.compareHash(srcData, bypassData)
	case KeyTypeList, KeyTypeSet:
		return d.compareList(srcData, bypassData)
	default:
		return false, errors.Errorf("unsupport type [%s]", keyType)
	}

}

// 对比string类型数据差异
func (d *DiffUtil) compareString(srcData interface{}, bypassData interface{}) (bool, error) {
	src, ok := srcData.(string)
	if !ok {
		return false, errors.Errorf("assert src data [%#v] to string failed", srcData)
	}
	bypass, ok := bypassData.(string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to string failed", bypassData)
	}

	if src == bypass {
		// 字符串相等直接返回true
		return true, nil
	}

	// 若不相等，尝试解析为json对比
	var s, b interface{}
	if err := jsonx.UnmarshalString(src, &s); err != nil {
		return false, nil
	}
	if err := jsonx.UnmarshalString(bypass, &b); err != nil {
		return false, nil
	}
	return jsonx.CompareObjects(s, b)
}

// 对比list/set类型数据差异
func (d *DiffUtil) compareList(srcData interface{}, bypassData interface{}) (bool, error) {
	srcList, ok := srcData.([]string)
	if !ok {
		return false, errors.Errorf("assert src data [%#v] to slice failed", srcData)
	}
	bypassList, ok := bypassData.([]string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to slice failed", bypassData)
	}
	if len(srcList) != len(bypassList) {
		return false, nil
	}

	equal, err := jsonx.CompareObjects(srcList, bypassList)
	if err != nil {
		return false, errors.Wrapf(err, "compare list/set [%v] and [%v] failed", srcList, bypassList)
	}
	if equal {
		return true, nil
	}

	var oList, bList []interface{}
	for _, src := range srcList {
		var s interface{}
		if err := jsonx.UnmarshalString(src, &s); err != nil {
			return false, nil
		}
		oList = append(oList, s)
	}

	for _, bypass := range bypassList {
		var b interface{}
		if err := jsonx.UnmarshalString(bypass, &b); err != nil {
			return false, nil
		}
		bList = append(bList, b)
	}

	return jsonx.CompareObjects(oList, bList)
}

// 对比hash类型数据差异
func (d *DiffUtil) compareHash(srcData interface{}, bypassData interface{}) (bool, error) {
	srcMap, ok := srcData.(map[string]string)
	if !ok {
		return false, errors.Errorf("assert src data [%#v] to map failed", srcData)
	}
	bypassMap, ok := bypassData.(map[string]string)
	if !ok {
		return false, errors.Errorf("assert bypass data [%#v] to map failed", bypassData)
	}

	equal, err := jsonx.CompareObjects(srcMap, bypassMap)
	if err != nil {
		return false, errors.Wrapf(err, "compare hash [%v] and [%v] failed", srcMap, bypassMap)
	}
	if equal {
		return true, nil
	}

	sKeySet := mapset.NewSet[string]()
	bKeySet := mapset.NewSet[string]()
	sMap := make(map[string]interface{})
	bMap := make(map[string]interface{})
	for k, v := range srcMap {
		var s interface{}
		if err := jsonx.UnmarshalString(v, &s); err != nil {
			return false, nil
		}
		sMap[k] = s
		sKeySet.Add(k)
	}

	for k, v := range bypassMap {
		var b interface{}
		if err := jsonx.UnmarshalString(v, &b); err != nil {
			return false, nil
		}
		bMap[k] = b
		bKeySet.Add(k)
	}

	if kOnlyInSrc := sKeySet.Difference(bKeySet).ToSlice(); len(kOnlyInSrc) != 0 {
		fmt.Printf("hash key [%v] only exsist in srcKey [%s]\n\n", kOnlyInSrc, d.SrcKey)
	}

	if kOnlyInBypass := bKeySet.Difference(sKeySet).ToSlice(); len(kOnlyInBypass) != 0 {
		fmt.Printf("hash key [%v] only exsist in bypassKey [%s]\n\n", kOnlyInBypass, d.BypassKey)
	}
	equal, _ = jsonx.CompareObjects(sMap, bMap)
	if equal {
		return true, nil
	}
	// 存在不一致，对比每个key获取详细差异

	for key := range sKeySet.Union(bKeySet).Iter() {
		sValue := srcMap[key]
		bValue := bypassMap[key]
		if equal, _ := jsonx.CompareJson(sValue, bValue); !equal {
			fmt.Printf("srcKey [%s] and bypassKey [%s] key [%s] value is different\n", d.SrcKey, d.BypassKey, key)
			fmt.Printf("srcKey [%s] key [%s] value [%s]\n", d.SrcKey, key, sValue)
			fmt.Printf("bypassKey [%s] key [%s] value [%s]\n\n", d.BypassKey, key, bValue)
		}

	}
	return false, nil
}
