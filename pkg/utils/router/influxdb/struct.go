// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"fmt"
	"sort"
	"strings"
)

// ClusterInfo cluster info with map
type ClusterInfo map[string]*Cluster

// Cluster info for influxdb，include host list and unreadable host list
type Cluster struct {
	HostList           []string `json:"host_list"`
	UnreadableHostList []string `json:"unreadable_host_list"`
}

// HostInfo host info with map
type HostInfo map[string]*Host

// Host info for influxdb, include host port and so on...
type Host struct {
	DomainName string `json:"domain_name"`
	Port       int    `json:"port"`
	GrpcPort   int    `json:"grpc_port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Protocol   string `json:"protocol"`
	// 兼容默认值为 false 需要保持开启，所以用反状态
	Disabled bool `json:"status,omitempty"`

	BackupRateLimit float64 `json:"backup_rate_limit,omitempty"`
	ReadRateLimit   float64 `json:"read_rate_limit,omitempty"`
}

type HostStatusInfo map[string]*HostStatus

// HostStatus Host info's status for influxdb, include read and last modify time
type HostStatus struct {
	Read           bool  `json:"read"`
	LastModifyTime int64 `json:"lastModifyTime"`
}

// TagInfo tag info with map
type TagInfo map[string]*Tag

// Tag info for influxdb conditions
type Tag struct {
	HostList       []string `json:"host_list"`
	UnreadableHost []string `json:"unreadable_host"`
}

type ProxyInfo map[string]*Proxy

type RetentionPolicy struct {
	IsDefault  bool `json:"is_default"`
	Resolution int  `json:"resolution"`
}

type Proxy struct {
	BKBizID           string                     `json:"bk_biz_id,omitempty"`
	DataID            string                     `json:"data_id,omitempty"`
	MeasurementType   string                     `json:"measurement_type,omitempty"`
	StorageID         string                     `json:"storageID,omitempty"`
	ClusterName       string                     `json:"clusterName"`
	TagsKey           []string                   `json:"tagsKey"`
	Db                string                     `json:"db"`
	Measurement       string                     `json:"measurement"`
	RetentionPolicies map[string]RetentionPolicy `json:"retention_policies,omitempty"`
	VmRt              string                     `json:"vm_rt,omitempty"`
}

type QueryRouterInfo map[string]*QueryRouter

type QueryRouter struct {
	BkBizId            string `json:"bk_biz_id"`
	DataId             string `json:"data_id"`
	MeasurementType    string `json:"measurement_type"`
	VmTableId          string `json:"vm_table_id"`
	BcsClusterId       string `json:"bcs_cluster_id"`
	IsInfluxdbDisabled bool   `json:"is_influxdb_disabled"`
}

type SpaceInfo map[string]Space

type FieldToResultTable map[string]ResultTableList

type DataLabelToResultTable map[string]ResultTableList

type ResultTableDetailInfo map[string]*ResultTableDetail

//go:generate msgp -tests=false
type Space map[string]*SpaceResultTable

//go:generate msgp -tests=false
type StableSpace []*SpaceResultTable

type SpaceResultTable struct {
	TableId string              `json:"table_id"`
	Filters []map[string]string `json:"filters"`
}

//go:generate msgp -tests=false
type ResultTableList []string

//go:generate msgp -tests=false
type ResultTableDetail struct {
	StorageId       int64    `json:"storage_id"`
	ClusterName     string   `json:"cluster_name"`
	DB              string   `json:"db"`
	TableId         string   `json:"table_id"`
	Measurement     string   `json:"measurement"`
	VmRt            string   `json:"vm_rt"`
	Fields          []string `json:"fields"`
	MeasurementType string   `json:"measurement_type"`
	BcsClusterID    string   `json:"bcs_cluster_id"`
	DataLabel       string   `json:"data_label"`
	TagsKey         []string `json:"tags_key"`
}

func (ss StableSpace) Len() int {
	return len(ss)
}

func (ss StableSpace) Less(i, j int) bool {
	return ss[i].TableId < ss[j].TableId
}

func (ss StableSpace) Swap(i, j int) {
	ss[i], ss[j] = ss[j], ss[i]
}

func (s *Space) Print() string {
	res := make([]string, 0)
	res = append(res, fmt.Sprint("--------------------------------"))
	for tableId, table := range *s {
		res = append(res, fmt.Sprintf("\t%-40s: %+v", tableId, table))
	}
	return strings.Join(res, "\n")
}

// Marshal 由于 Space 是无序字典，无法保证每一次的序列化的内容是稳定的，需要在序列化过程中，将其转换为有序的切片对象
func (s *Space) Marshal(b []byte) (o []byte, err error) {
	ss := StableSpace{}
	for _, table := range *s {
		ss = append(ss, table)
		// 排序保证结构稳定
		sort.Sort(ss)
	}
	return (&ss).MarshalMsg(b)
}

// Unmarshal 由于 Space 是无序字典，内部存的是切片对象 StableSpace，反序列化过程需要做对象转换
func (s *Space) Unmarshal(bts []byte) (o []byte, err error) {
	ss := StableSpace{}
	o, err = (&ss).UnmarshalMsg(bts)
	if err != nil {
		return
	}
	for _, table := range ss {
		(*s)[table.TableId] = table
	}
	return
}

func (rt *ResultTableDetail) Print() string {
	return fmt.Sprintf("%+v", *rt)
}

func (rt *ResultTableDetail) Marshal(b []byte) (o []byte, err error) {
	return rt.MarshalMsg(b)
}

func (rt *ResultTableDetail) Unmarshal(bts []byte) (o []byte, err error) {
	return rt.UnmarshalMsg(bts)
}

func (rtList *ResultTableList) Print() string {
	return fmt.Sprintf("%+v", *rtList)
}

func (rtList *ResultTableList) Marshal(b []byte) (o []byte, err error) {
	return rtList.MarshalMsg(b)
}

func (rtList *ResultTableList) Unmarshal(bts []byte) (o []byte, err error) {
	return rtList.UnmarshalMsg(bts)
}

func (info SpaceInfo) NewValueInstance() GenericValue {
	return &Space{}
}

func (info SpaceInfo) SetValueInstance(key string, value GenericValue) {
	space := *value.(*Space)
	// 将 KEY 置于结构体，内容更为完整
	for tableId, table := range space {
		table.TableId = tableId
	}
	info[key] = space
}

func (info FieldToResultTable) NewValueInstance() GenericValue {
	return &ResultTableList{}
}

func (info FieldToResultTable) SetValueInstance(key string, value GenericValue) {
	info[key] = *value.(*ResultTableList)
}

func (info DataLabelToResultTable) NewValueInstance() GenericValue {
	return &ResultTableList{}
}

func (info DataLabelToResultTable) SetValueInstance(key string, value GenericValue) {
	info[key] = *value.(*ResultTableList)
}

func (info ResultTableDetailInfo) NewValueInstance() GenericValue {
	return &ResultTableDetail{}
}

func (info ResultTableDetailInfo) SetValueInstance(key string, value GenericValue) {
	table := value.(*ResultTableDetail)
	table.TableId = key
	info[key] = table
}

type GenericHash interface {
	NewValueInstance() GenericValue
	SetValueInstance(key string, value GenericValue)
}

type GenericValue interface {
	Marshal(b []byte) (o []byte, err error)
	Unmarshal(bts []byte) (o []byte, err error)
	Print() string
}
