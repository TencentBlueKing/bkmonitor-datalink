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

type SpaceInfo map[string]Space

type FieldToResultTable map[string]ResultTableList

type DataLabelToResultTable map[string]ResultTableList

type ResultTableDetailInfo map[string]*ResultTableDetail

//go:generate msgp -tests=false
type BkAppSpace map[string]*SpaceUIDList

type SpaceUIDList []string

func (z *SpaceUIDList) Marshal(b []byte) (o []byte, err error) {
	return z.MarshalMsg(b)
}

func (z *SpaceUIDList) Unmarshal(bts []byte) (o []byte, err error) {
	return z.UnmarshalMsg(bts)
}

func (z *SpaceUIDList) Print() string {
	return fmt.Sprintf("%+v", *z)
}

func (z *SpaceUIDList) Length() int {
	return len(*z)
}

func (z *SpaceUIDList) Fill(key string) {
}

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

type TimeField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Unit string `json:"unit"`
}

type Record struct {
	StorageID  int64 `json:"storage_id,omitempty"`
	EnableTime int64 `json:"enable_time,omitempty"`
}

//go:generate msgp -tests=false
type ResultTableDetail struct {
	StorageId             int64             `json:"storage_id"`
	StorageName           string            `json:"storage_name"`
	StorageType           string            `json:"storage_type"`
	StorageClusterRecords []Record          `json:"storage_cluster_records"`
	ClusterName           string            `json:"cluster_name"`
	DB                    string            `json:"db"`
	TableId               string            `json:"table_id"`
	Measurement           string            `json:"measurement"`
	VmRt                  string            `json:"vm_rt"`
	CmdbLevelVmRt         string            `json:"cmdb_level_vm_rt"`
	Fields                []string          `json:"fields"`
	FieldAlias            map[string]string `json:"field_alias"` // 字段别名，key 为新字段，value 为老字段
	MeasurementType       string            `json:"measurement_type"`
	BcsClusterID          string            `json:"bcs_cluster_id"`
	DataLabel             string            `json:"data_label"`
	TagsKey               []string          `json:"tags_key"`
	DataId                int64             `json:"bk_data_id"`
	SourceType            string            `json:"source_type"`
	Options               struct {
		// 自定义时间聚合字段
		TimeField TimeField `json:"time_field"`
		// db 是否拼接时间格式
		NeedAddTime bool `json:"need_add_time"`
	} `json:"options"`
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

func (s *Space) Length() int {
	return len(*s)
}

// Marshal 由于 Space 是无序字典，无法保证每一次的序列化的内容是稳定的，需要在序列化过程中，将其转换为有序的切片对象
func (s *Space) Marshal(b []byte) (o []byte, err error) {
	ss := StableSpace{}
	for _, table := range *s {
		ss = append(ss, table)
	}
	// 排序保证结构稳定
	sort.Sort(ss)
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

func (s *Space) Fill(key string) {
	for tableId, table := range *s {
		table.TableId = tableId
	}
}

func (rt *ResultTableDetail) Print() string {
	return fmt.Sprintf("%+v", *rt)
}

func (rt *ResultTableDetail) Length() int {
	return 1
}

func (rt *ResultTableDetail) Marshal(b []byte) (o []byte, err error) {
	return rt.MarshalMsg(b)
}

func (rt *ResultTableDetail) Unmarshal(bts []byte) (o []byte, err error) {
	return rt.UnmarshalMsg(bts)
}

func (rt *ResultTableDetail) Fill(key string) {
	rt.TableId = key
}

func (rtList *ResultTableList) Print() string {
	return fmt.Sprintf("%+v", *rtList)
}

func (rtList *ResultTableList) Length() int {
	return len(*rtList)
}

func (rtList *ResultTableList) Marshal(b []byte) (o []byte, err error) {
	return rtList.MarshalMsg(b)
}

func (rtList *ResultTableList) Unmarshal(bts []byte) (o []byte, err error) {
	return rtList.UnmarshalMsg(bts)
}

func (rtList *ResultTableList) Fill(key string) {}

type GenericValue interface {
	Marshal(b []byte) (o []byte, err error)
	Unmarshal(bts []byte) (o []byte, err error)
	Print() string
	Length() int
	Fill(key string)
}
