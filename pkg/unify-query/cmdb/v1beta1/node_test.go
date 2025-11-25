// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestResourceType_IDAndName(t *testing.T) {
	rt := newResourceType()

	// 测试获取资源类型ID
	id1 := rt.id("host")
	assert.Equal(t, uint16(1), id1)

	// 测试重复获取相同资源类型ID
	id2 := rt.id("host")
	assert.Equal(t, id1, id2)

	// 测试获取不同资源类型ID
	id3 := rt.id("service")
	assert.Equal(t, uint16(2), id3)

	// 测试通过ID获取名称
	name1 := rt.name(id1)
	assert.Equal(t, cmdb.Resource("host"), name1)

	name2 := rt.name(id3)
	assert.Equal(t, cmdb.Resource("service"), name2)

	// 测试不存在的ID
	name3 := rt.name(999)
	assert.Equal(t, cmdb.Resource(""), name3)
}

func TestNodeBuilder_GetID(t *testing.T) {
	nb := NewNodeBuilder()

	// 测试第一次获取ID
	info1 := cmdb.Matcher{
		"bcs_cluster_id": "BCS-K8S-00000",
		"namespace":      "blueking",
		"pod":            "test-pod",
		"container":      "test-container",
	}

	id1, err := nb.GetID("pod", info1)
	assert.NoError(t, err)
	assert.NotEqual(t, id1, uint64(0))

	// 测试相同信息获取相同ID
	id2, err := nb.GetID("pod", info1)
	assert.NoError(t, err)
	assert.Equal(t, id1, id2)

	// 测试不同信息获取不同ID
	info2 := cmdb.Matcher{
		"bcs_cluster_id": "BCS-K8S-00000",
		"namespace":      "blueking",
		"pod":            "test-pod-1",
		"container":      "test-container",
	}
	id3, err := nb.GetID("pod", info2)
	assert.NoError(t, err)
	assert.NotEqual(t, id1, id3)

	// 测试不同资源类型获取不同ID
	id4, err := nb.GetID("container", info1)
	assert.NoError(t, err)
	assert.NotEqual(t, id1, id4)

	// 测试错误情况：resourceType为空
	_, err = nb.GetID("", info1)
	assert.Error(t, err)

	// 测试错误情况：info为空
	_, err = nb.GetID("container", nil)
	assert.Error(t, err)
}

func TestNodeBuilder_Info(t *testing.T) {
	nb := NewNodeBuilder()

	info := cmdb.Matcher{
		"bk_target_ip": "127.0.0.1",
	}

	// 获取ID
	id, err := nb.GetID("system", info)
	assert.NoError(t, err)

	// 测试Info方法
	resourceName, retrievedInfo := nb.Info(id)
	assert.Equal(t, cmdb.Resource("system"), resourceName)
	assert.Equal(t, info, retrievedInfo)

	// 测试不存在的ID
	resourceName2, retrievedInfo2 := nb.Info(999999)
	assert.Equal(t, cmdb.Resource(""), resourceName2)
	assert.Nil(t, retrievedInfo2)
}

func TestNodeBuilder_IDStructure(t *testing.T) {
	nb := NewNodeBuilder()

	info := cmdb.Matcher{
		"key": "value",
	}

	id, err := nb.GetID("test-resource", info)
	assert.NoError(t, err)

	// 验证ID结构：前16位是资源类型ID，后48位是索引
	resourceTypeID := uint16(id >> 48)
	index := id & 0xFFFFFFFFFFFF

	assert.Equal(t, uint16(1), resourceTypeID)
	assert.Equal(t, uint64(1), index)

	// 测试第二个ID
	info2 := cmdb.Matcher{
		"key2": "value2",
	}
	id2, err := nb.GetID("test-resource", info2)
	assert.NoError(t, err)
	resourceTypeID2 := uint16(id2 >> 48)
	index2 := id2 & 0xFFFFFFFFFFFF

	assert.Equal(t, uint16(1), resourceTypeID2) // 相同资源类型
	assert.Equal(t, uint64(2), index2)          // 索引递增
}

func TestNodeBuilder_ConcurrentAccess(t *testing.T) {
	nb := NewNodeBuilder()

	// 并发测试
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			info := cmdb.Matcher{
				"index": string(rune(index)),
			}
			id, err := nb.GetID("concurrent", info)
			assert.NoError(t, err)
			_, retrievedInfo := nb.Info(id)
			assert.Equal(t, info, retrievedInfo)
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestResourceType_ConcurrentAccess(t *testing.T) {
	rt := newResourceType()

	// 并发测试资源类型
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			resourceName := string(rune('A' + index))
			id := rt.id(cmdb.Resource(resourceName))
			name := rt.name(id)
			assert.Equal(t, cmdb.Resource(resourceName), name)
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestNodeBuilder_ResourceNodeInfo(t *testing.T) {
	nb := NewNodeBuilder()

	// 添加多个相同资源类型的节点
	info1 := cmdb.Matcher{"ip": "127.0.0.1"}
	info2 := cmdb.Matcher{"ip": "192.168.1.1"}
	info3 := cmdb.Matcher{"ip": "10.0.0.1"}

	_, err := nb.GetID("host", info1)
	assert.NoError(t, err)
	_, err = nb.GetID("host", info2)
	assert.NoError(t, err)
	_, err = nb.GetID("service", info3) // 不同资源类型
	assert.NoError(t, err)

	// 获取host资源类型的所有节点信息
	hostInfos := nb.ResourceNodeInfo("host")
	assert.Len(t, hostInfos, 2)
	assert.Contains(t, hostInfos, info1)
	assert.Contains(t, hostInfos, info2)
	assert.NotContains(t, hostInfos, info3)

	// 获取不存在的资源类型
	emptyInfos := nb.ResourceNodeInfo("nonexistent")
	assert.Len(t, emptyInfos, 0)
}

func TestNodeBuilder_Length(t *testing.T) {
	nb := NewNodeBuilder()

	// 初始长度应为0
	assert.Equal(t, 0, nb.Length())

	// 添加节点后长度增加
	info := cmdb.Matcher{"key": "value"}
	_, err := nb.GetID("test", info)
	assert.NoError(t, err)
	assert.Equal(t, 1, nb.Length())

	// 添加相同节点长度不变
	_, err = nb.GetID("test", info)
	assert.NoError(t, err)
	assert.Equal(t, 1, nb.Length())

	// 添加不同节点长度增加
	info2 := cmdb.Matcher{"key2": "value2"}
	_, err = nb.GetID("test", info2)
	assert.NoError(t, err)
	assert.Equal(t, 2, nb.Length())
}

func TestNodeBuilder_Clean(t *testing.T) {
	nb := NewNodeBuilder()

	// 添加一些节点
	info := cmdb.Matcher{"key": "value"}
	_, err := nb.GetID("test", info)
	assert.NoError(t, err)
	assert.Equal(t, 1, nb.Length())

	// 清理后长度应为0
	nb.Clean()
	assert.Equal(t, 0, nb.Length())

	// 清理后Info方法应返回空值
	id, err := nb.GetID("test", info)
	assert.NoError(t, err)
	resourceName, retrievedInfo := nb.Info(id)
	assert.Equal(t, cmdb.Resource("test"), resourceName)
	assert.Equal(t, info, retrievedInfo)
}
