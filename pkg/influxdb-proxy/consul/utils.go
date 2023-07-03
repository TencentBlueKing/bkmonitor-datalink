// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"bytes"
	"crypto/sha1"
	"encoding/gob"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// HashIt : hash an object
func hashIt(object interface{}) string {
	var (
		buf     bytes.Buffer
		encoder = gob.NewEncoder(&buf)
	)

	err := encoder.Encode(object)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha1.Sum(buf.Bytes()))
}

// true 大于 false 小于
func compareString(str1 string, str2 string) bool {
	length1 := len(str1)
	length2 := len(str2)
	if length1 > length2 {
		return true
	}
	if length1 < length2 {
		return false
	}
	// 长度相等则深度判断
	byte1 := []byte(str1)
	byte2 := []byte(str2)
	for i := 0; i < length1; i++ {
		if byte1[i] > byte2[i] {
			return true
		}
		if byte1[i] < byte2[i] {
			return false
		}
	}
	return false
}

// bubbleSortStringList 冒泡排序
func bubbleSortStringList(list []string) []string {
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if compareString(list[i], list[j]) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	return list
}

// 对kvpair排序,针对key进行排序
func sortKVPairs(kvPairs api.KVPairs, watchPath string) api.KVPairs {
	// 将kvpair按key存储，然后收集一个key列表
	mapInfo := make(map[string]*api.KVPair)
	keyList := make([]string, 0, len(kvPairs))
	for _, value := range kvPairs {
		// 屏蔽watch路径，因为这个始终有更新
		if watchPath != "" && value.Key == watchPath {
			continue
		}
		mapInfo[value.Key] = value
		keyList = append(keyList, value.Key)
	}
	// 获取key的排序结果
	ordedList := bubbleSortStringList(keyList)
	// 根据key的排序,重新生成一个kvpairs
	orderedKVPairs := make(api.KVPairs, 0, len(kvPairs))
	for _, v := range ordedList {
		kvPair := mapInfo[v]
		orderedKVPairs = append(orderedKVPairs, kvPair)
	}

	return orderedKVPairs
}

func kvToRouteInfo(kvPair *api.KVPair) (*RouteInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	info := kvPair.Value

	ti := new(RouteInfo)
	err := json.Unmarshal(info, ti)
	if err != nil {
		flowLog.Errorf("unmarshal route info failed,key:%v,value:%v,error:%v", kvPair.Key, string(info), err)
		return nil, err
	}
	return ti, nil
}

func kvToClusterInfo(kvPair *api.KVPair) (*ClusterInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	info := kvPair.Value

	ci := new(ClusterInfo)
	err := json.Unmarshal(info, ci)
	if err != nil {
		flowLog.Errorf("unmarshal cluster info failed,key:%v,value:%v,error:%v", kvPair.Key, string(info), err)
		return nil, err
	}
	return ci, nil
}

func kvToHostInfo(kvPair *api.KVPair) (*HostInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	info := kvPair.Value

	hi := new(HostInfo)
	err := json.Unmarshal(info, hi)
	if err != nil {
		flowLog.Errorf("unmarshal host info failed,key:%v,value:%v,error:%v", kvPair.Key, string(info), err)
		return nil, err
	}
	return hi, nil
}

func kvToTagInfo(kvPair *api.KVPair) (*TagInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	info := kvPair.Value

	ti := new(TagInfo)
	err := json.Unmarshal(info, ti)
	if err != nil {
		flowLog.Errorf("unmarshal tag info failed,key:%v,value:%v,error:%v", kvPair.Key, string(info), err)
		return nil, err
	}
	return ti, nil
}
