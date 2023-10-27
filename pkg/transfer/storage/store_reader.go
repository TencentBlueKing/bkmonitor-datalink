// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
)

type CacheReader interface {
	Get(key string) ([]byte, error)
	// Scan is a read only method
	Scan(prefix string, callback define.StoreScanCallback, withTime ...bool) error
	Close() error
}

type cmdbReader struct {
	r CacheReader
}

// Filter 使用store接口，过滤数据, return hostCache, instCache, err
func (cr *cmdbReader) Filter(ip string, cloudID int) (map[string]*define.StoreItem, map[string]*define.StoreItem, error) {
	var (
		ccPrefix  string
		err       error
		hostCache = make(map[string]*define.StoreItem)
		instCache = make(map[string]*define.StoreItem)
	)

	logging.Infof("filter ip:[%s], cloudID:[%d]", ip, cloudID)
	// 如果cloud和ip都确定，则可以确定唯一key，直接使用Get方法。
	if cloudID != -1 && ip != "" {
		hostKey := fmt.Sprintf("%s-%d-%s", models.HostInfoStorePrefix, cloudID, ip)

		targetBytes, err := cr.r.Get(hostKey)
		if err != nil {
			logging.Errorf("cache reader get host:[%s] error:[%s]", hostKey, err)
			return nil, nil, err
		}
		hostCache = make(map[string]*define.StoreItem, 1)
		// 由于store的Get接口返回的只有data信息，所以此处的过期时间是没法直接通过key获取到的
		// 因此这里提供的是一个假的过期时间
		hostCache[hostKey] = &define.StoreItem{
			Data:      targetBytes,
			ExpiresAt: new(time.Time),
		}

		return hostCache, nil, err
	}

	// 云区域或者ip有一方不为空，则代表需要过滤主机。
	if cloudID != -1 || ip != "" {
		ccPrefix = models.HostInfoStorePrefix
	}

	// 如果都为空，则不过滤
	if cloudID == -1 && ip == "" {
		ccPrefix = ""
	}

	hostCache = make(map[string]*define.StoreItem)
	instCache = make(map[string]*define.StoreItem)
	err = cr.r.Scan(ccPrefix, func(key string, data []byte) bool {
		item, innerErr := unmarshalStoreItem(data)
		if innerErr != nil {
			err = innerErr
			return true
		}
		// 检测主机类的数据
		if strings.HasPrefix(key, models.HostInfoStorePrefix) {
			hostInfo, innerErr := unmarshalHostInfo(item.GetData(false))
			if innerErr != nil {
				logging.Warnf("unmarshal host info:[%s] error in cache reader :[%s]",
					string(item.GetData(false)), innerErr)
				err = innerErr
				return true
			}

			// 过滤ip和cloudid
			if ip != "" && hostInfo.IP != ip {
				return true
			}
			if cloudID != -1 && hostInfo.CloudID != cloudID {
				return true
			}

			hostCache[key] = item
			return true
		}

		// 非主机类型
		if strings.HasPrefix(key, models.InstanceInfoStorePrefix) {
			instCache[key] = item
		}
		return true
	}, true)
	return hostCache, instCache, err
}

// All: return hostCache, instCache, err
func (cr *cmdbReader) All() (map[string]*define.StoreItem, map[string]*define.StoreItem, error) {
	return cr.Filter("", -1)
}

// MemCache: return transfer memory cache
func (cr *cmdbReader) MemCache(addr, storeType string) (map[string]*define.StoreItem, error) {
	url := addr + "/cache"
	reqBody := map[string]string{
		"store_type": storeType,
	}
	reqContent, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("error occured marshal reqBody:[%v], err: [%s]", reqBody, err)
	}
	r := bytes.NewReader(reqContent)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", r)
	if err != nil {
		return nil, fmt.Errorf("error occured request url: [%s], err: [%s]", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error get HostData ,status:[%d]", resp.StatusCode)
	}

	var respContent define.RespCacheData
	var content []byte

	if content, err = io.ReadAll(resp.Body); err != nil {
		return nil, fmt.Errorf("error occured read response body:[%#v], err:[%s]", resp.Body, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if err = json.Unmarshal(content, &respContent); err != nil {
		return nil, fmt.Errorf("error occured unmarshal response content:[%#v], err:[%s]", content, err)
	}

	if !respContent.Result {
		return nil, fmt.Errorf("error get hotData, %s", respContent.Message)
	}
	return respContent.Data, nil
}

// Close:
func (cr *cmdbReader) Close() error {
	return cr.r.Close()
}

// NewReaderHelper:
func NewReaderHelper(ctx context.Context, name string) (*cmdbReader, error) {
	var (
		store define.Store
		err   error
	)

	if s := define.StoreFromContext(ctx); s != nil {
		store = s
	} else {
		// 根据 storeType 获取store
		store, err = define.NewStore(ctx, name)
	}

	if err != nil {
		logging.Errorf("new store error:[%s]", err)
		return nil, err
	}

	return &cmdbReader{
		r: store,
	}, nil
}

func unmarshalStoreItem(v []byte) (*define.StoreItem, error) {
	var item *define.StoreItem
	err := json.Unmarshal(v, &item)
	if err != nil {
		logging.Warnf("unmarshal store item:[%s], error:[%s]", string(v), err)
		return new(define.StoreItem), err
	}
	return item, nil
}

func unmarshalHostInfo(v []byte) (models.CCHostInfo, error) {
	var topo models.CCHostInfo
	err := json.Unmarshal(v, &topo)
	if err != nil {
		logging.Warnf("unmarsal host info:[%s], error:[%s]", string(v), err)
		return models.CCHostInfo{}, err
	}
	return topo, nil
}
