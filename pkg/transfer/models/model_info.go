// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"bytes"
	"fmt"
	"time"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// HostInfoStorePrefix:
var (
	HostInfoStorePrefix      = "model-host"
	AgentHostInfoStorePrefix = "model-agent-host"
	InstanceInfoStorePrefix  = "model-instance"
)

// cc k-v 存储
type CCInfo interface {
	// 返回key
	GetStoreKey() string
	// 将data 压入 info 中
	LoadByBytes(data []byte) error
	// 将store 数据 压入info中
	LoadStore(store define.Store) error
	// 将store 数据 压入info中 by 指定Key
	LoadStoreKey(store define.Store, key string) error
	// 将Info 的数据写到store 中
	Dump(store define.Store, expires time.Duration) error
	// 返回执行模板
	CCTemplateInfo
}

type CCTemplateInfo interface {
	GetInfo() CCTemplateInfo
}

// topo 层级执行模板
type CCTopoBaseModelInfo struct {
	Topo  []map[string]string
	BizID []int
}

type CCTopoV3ModelInfo struct {
	Topo  []map[string]interface{}
	BizID int
}

func (modelInfo *CCTopoBaseModelInfo) GetInfo() CCTemplateInfo {
	return modelInfo
}

// 按照Host 上报cc cache 结构
type CCHostInfo struct {
	*CCTopoBaseModelInfo
	IP           string `json:"ip"`
	CloudID      int    `json:"bk_cloud_id"`
	OuterIP      string `json:"outer_ip,omitempty"`
	DbmMeta      string `json:"dbm_meta"`
	DevxMeta     string `json:"devx_meta"`
	PerforceMeta string `json:"perforce_meta"`
}

// 返回前缀 + cloud ID + IP
func (h *CCHostInfo) GetStoreKey() string {
	return fmt.Sprintf("%s-%d-%s", HostInfoStorePrefix, h.CloudID, h.IP)
}

// 格式化key
func SetHostKey(ip string, cloudID int) string {
	return fmt.Sprintf("%s-%d-%s", HostInfoStorePrefix, cloudID, ip)
}

func (h *CCHostInfo) LoadByBytes(data []byte) error {
	return ModelConverter.Unmarshal(data, h)
}

func (h *CCHostInfo) LoadStore(store define.Store) error {
	data, err := store.Get(h.GetStoreKey())
	if err != nil {
		return err
	}
	return ModelConverter.Unmarshal(data, h)
}

func (h *CCHostInfo) LoadStoreKey(store define.Store, key string) error {
	data, err := store.Get(key)
	if err != nil {
		return err
	}
	return ModelConverter.Unmarshal(data, h)
}

func (h *CCHostInfo) Dump(store define.Store, expires time.Duration) error {
	data, err := ModelConverter.Marshal(h)
	if err != nil {
		return err
	}
	return store.PutCache(h.GetStoreKey(), data, expires)
}

func NewHostInfoWithTemplate(templateInfo func() *CCTopoBaseModelInfo) CCHostInfo {
	return CCHostInfo{
		CCTopoBaseModelInfo: templateInfo(),
	}
}

func NewInstanceInfoWithTemplate(templateInfo func() *CCTopoBaseModelInfo) CCInstanceInfo {
	return CCInstanceInfo{
		CCTopoBaseModelInfo: templateInfo(),
	}
}

// 按照实例 上报cc cache 结构
type CCInstanceInfo struct {
	*CCTopoBaseModelInfo
	InstanceID string `json:"instance_id"`
	OuterIP    string `json:"outer_ip,omitempty"`
}

// 返回前缀 + instance id
func (i *CCInstanceInfo) GetStoreKey() string {
	return fmt.Sprintf("%s-%s", InstanceInfoStorePrefix, i.InstanceID)
}

func (i *CCInstanceInfo) LoadByBytes(data []byte) error {
	return ModelConverter.Unmarshal(data, i)
}

func (i *CCInstanceInfo) LoadStore(store define.Store) error {
	data, err := store.Get(i.GetStoreKey())
	if err != nil {
		return err
	}
	return ModelConverter.Unmarshal(data, i)
}

func (i *CCInstanceInfo) LoadStoreKey(store define.Store, key string) error {
	data, err := store.Get(key)
	if err != nil {
		return err
	}
	return ModelConverter.Unmarshal(data, i)
}

func (i *CCInstanceInfo) Dump(store define.Store, expires time.Duration) error {
	data, err := ModelConverter.Marshal(i)
	if err != nil {
		return err
	}
	return store.PutCache(i.GetStoreKey(), data, expires)
}

type CCAgentHostInfo struct {
	AgentID string `json:"bk_agent_id"`
	IP      string `json:"bk_host_innerip"`
	CloudID int    `json:"bk_cloud_id"`
	BizID   int    `json:"bk_biz_id"`
}

func (h *CCAgentHostInfo) GetInfo() CCTemplateInfo {
	return h
}

func (h *CCAgentHostInfo) GetStoreKey() string {
	return fmt.Sprintf("%s-%s", AgentHostInfoStorePrefix, h.AgentID)
}

// LoadByBytes : format is "bk_biz_id:cloud_id:ip"
func (h *CCAgentHostInfo) LoadByBytes(data []byte) error {
	parts := bytes.Split(data, []byte(":"))
	if len(parts) != 3 {
		return fmt.Errorf("invalid data format: %s", string(data))
	}

	h.BizID = conv.Int(string(parts[0]))
	h.CloudID = conv.Int(string(parts[1]))
	h.IP = string(parts[2])
	return nil
}

func (h *CCAgentHostInfo) LoadStore(store define.Store) error {
	data, err := store.Get(h.GetStoreKey())
	if err != nil {
		return err
	}
	return h.LoadByBytes(data)
}

func (h *CCAgentHostInfo) LoadStoreKey(store define.Store, key string) error {
	data, err := store.Get(key)
	if err != nil {
		return err
	}
	return h.LoadByBytes(data)
}

func (h *CCAgentHostInfo) Dump(store define.Store, expires time.Duration) error {
	data := []byte(fmt.Sprintf("%d:%d:%s", h.BizID, h.CloudID, h.IP))
	return store.PutCache(h.GetStoreKey(), data, expires)
}
