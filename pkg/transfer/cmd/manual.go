// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func addManual(target, dataID, manualPath string, client consul.SourceClient) {
	keys, err := client.GetKeys(manualPath)
	checkError(err, -1, "get manual keys failed")

	targetPath := path.Join(manualPath, dataID)
	// 查找路径下是否已经有对应key
	hasKey := false
	for _, key := range keys {
		if targetPath == key {
			hasKey = true
			break
		}
	}

	// 没有key则新增
	if !hasKey {
		manualList := make([]map[string]string, 0, 1)
		manualList = append(manualList, map[string]string{
			"name": target,
		})
		value, err := json.Marshal(manualList)
		checkError(err, -1, "marshal json failed")
		err = client.Put(targetPath, value)
		checkError(err, -1, "put manual data failed")
		return
	}

	// 有key则在key的内容中更新，增加对应的项
	value, _ := client.Get(targetPath)
	var manualList []map[string]string
	err = json.Unmarshal(value, &manualList)
	checkError(err, -1, "unmarshal json failed")
	manualList = append(manualList, map[string]string{
		"name": target,
	})
	changedValue, err := json.Marshal(manualList)
	checkError(err, -1, "marshal json failed")
	err = client.Put(targetPath, changedValue)
	checkError(err, -1, "put manual data failed")
}

func deleteManual(target, dataID, manualPath string, client consul.SourceClient) {
	// 获取指定路径的数据
	targetPath := path.Join(manualPath, dataID)
	value, err := client.Get(targetPath)
	checkError(err, -1, "get manual value failed")

	// 将数据反序列化，获得maps
	var manualList []map[string]string
	err = json.Unmarshal(value, &manualList)
	checkError(err, -1, "unmarshal json failed")

	// 遍历数据，过滤掉要删除的项
	resultList := make([]map[string]string, 0, len(manualList)-1)
	for _, manual := range manualList {
		if manual["name"] == target {
			continue
		}
		resultList = append(resultList, manual)
	}

	// 如果遍历后的结果数据为空，则直接删除这个key
	if len(resultList) == 0 {
		checkError(client.Delete(targetPath), -1, "delete manual path failed:%s", targetPath)
		return
	}

	// 将数据回写到consul
	changedValue, err := json.Marshal(resultList)
	checkError(err, -1, "marshal json failed")
	err = client.Put(targetPath, changedValue)
	checkError(err, -1, "put manual data failed")
}

func listManual(client consul.SourceClient, manualPath, pathVersion string) map[string]string {
	// 获取keys，逐行打印
	var (
		table       = utils.NewTableUtil(os.Stdout, false)
		value       []byte
		serviceInfo []map[string]string // consul中的信息内容
		serviceList []string            // 最终需要打印的内容
	)

	// 拼接所有集群manualPath，获取路径，在获取keys
	manualPrefix, _ := path.Split(strings.Trim(manualPath, "/"))
	// xxx/manual/v1/$cluster   xxx/manual
	clusterPaths, err := client.GetKeys(manualPrefix)
	checkError(err, -1, "get cluster failed")

	tableHeader := parseTableByPathVersion(pathVersion, []string{"CLUSTER", "DATA_ID", "SERVICE_LIST"})
	table.SetHeader(tableHeader)

	for _, clusterPath := range clusterPaths {
		// 去除非 "/" 结尾的path
		if !strings.HasSuffix(clusterPath, "/") {
			continue
		}
		// xxx/manual/v1/$cluster/    xxx/manual/
		cluster_keys, err := client.GetKeys(clusterPath)
		checkError(err, -1, "get manual keys failed")

		clusterName := path.Base(clusterPath)
		for _, key := range cluster_keys {
			serviceList = make([]string, 0)

			if value, err = client.Get(key); err != nil {
				tableRow := parseTableByPathVersion(pathVersion, []string{clusterName, path.Base(key), string(value)})
				table.Append(tableRow)
				continue
			}

			if err = json.Unmarshal(value, &serviceInfo); err != nil {
				tableRow := parseTableByPathVersion(pathVersion, []string{clusterName, path.Base(key), string(value)})
				table.Append(tableRow)
				continue
			}

			for _, service := range serviceInfo {
				serviceList = append(serviceList, service[consul.ConfServiceNameKey])
			}

			tableRow := parseTableByPathVersion(pathVersion, []string{clusterName, path.Base(key), strings.Join(serviceList, " | ")})
			table.Append(tableRow)
		}
	}

	fmt.Println("manual path:", manualPath)
	checkError(err, -1, "get manual keys failed")

	table.Render()

	return nil
}

// 将v0 版本的手动分配data_id迁移到v1版本的手动分配data_id路径上
func migrateV0ToV1Path(client consul.SourceClient, passingServices map[string]*define.ServiceInfo, v0Path, v1Path, defaultDataIDPath string, assumeyes bool) {
	fmt.Println("origin manual path:", v0Path)

	var (
		table       = utils.NewTableUtil(os.Stdout, false)
		v0DataIDMap = listManualDataID(client, v0Path) // v0路径规则下手动分配data_id列表
		v1DataIDMap = listManualDataID(client, v1Path) // v1路径规则下手动分配data_id列表
		allKeys     []string                           // default集群下所有data_id
	)

	// 获取v1版本default集群下所有data_id
	allKeys, err := client.GetKeys(defaultDataIDPath)
	checkError(err, 1, "get data_id error")

	// 构造map，减少遍历查找次数
	tmpDataIDMap := make(map[string]string, len(allKeys))
	for _, key := range allKeys {
		tmpDataIDMap[key] = "yes"
	}

	// 判断dataid，service在当前环境是否存在，返回不存在的data_id，service
	filterFn := func(dataid string, services []string) (string, bool) {
		var notExist string
		isConflict := false
		first := true
		// 判断v0版本中的data_id 在v1的集群下是否存在
		if _, ok := tmpDataIDMap[dataid]; !ok {
			notExist += "dataid: " + dataid
		}

		// 判断v0版本中data_id是否和v1版本中有冲突
		if _, ok := v1DataIDMap[dataid]; ok {
			isConflict = true
		}

		// 判断v0版本的手动分配data_id中的transfer实例是否都还存活
		for _, service := range services {
			if _, ok := passingServices[service]; !ok {
				if first {
					notExist += "  ,services: " + service
					first = false
				} else {
					notExist += service
				}
			}
		}

		return notExist, isConflict
	}

	table.SetHeader([]string{"DATA ID", "SERVICE LIST", "NOT EXIST DATA ID", "NOT EXIST SERVICE LIST"})
	v0DataIDCount := len(v0DataIDMap)
	if v0DataIDCount > 0 {
		fmt.Println("warning: ", "The dataids in the following table will be added to the V1-pathVersion")
	}

	for dataid, services := range v0DataIDMap {
		notExist, _ := filterFn(dataid, services)
		table.Append([]string{dataid, strings.Join(services, "|"), notExist})
	}
	table.SetCaption(true, fmt.Sprintf("%v dataid will be added to  V1-pathVersion", v0DataIDCount))
	table.Render()

	// 迁移动作是否执行
	if !assumeyes {
		return
	}
	for dataid, services := range v0DataIDMap {
		for _, service := range services {
			addManual(service, dataid, v1Path, client)
		}
	}
}

// 列出路径下所有data_id：{dataid:[serviceInstances]}
func listManualDataID(client consul.SourceClient, manualPath string) map[string][]string {
	var (
		keys        []string
		value       []byte
		serviceInfo []map[string]string
		dataIDMap   = make(map[string][]string)
	)

	// xx/manual/,  xx/manual/v1/$cluster/
	manualPath = manualPath + "/"
	keys, err := client.GetKeys(manualPath)
	checkError(err, 1, "get manual error")

	for _, key := range keys {
		services := make([]string, 0)

		if value, err = client.Get(key); err != nil {
			continue
		}

		// [{"name":"bkmonitorv3-663687732"}]
		if err = json.Unmarshal(value, &serviceInfo); err != nil {
			continue
		}

		for _, service := range serviceInfo {
			serviceName := service[consul.ConfServiceNameKey]
			services = append(services, serviceName)
		}

		dataID := path.Base(key)
		dataIDMap[dataID] = services
	}
	return dataIDMap
}

func getManualV0Path(conf define.Configuration) string {
	pathVersion := conf.GetString(consul.ConfKeyPathVersion)
	switch pathVersion {
	case "":
		// v0 版本
		return conf.GetString(consul.ConfKeyManualPath)
	default:
		// v1 版本  xxx/manual/v1/$cluster => xxx/manual
		manualPath := conf.GetString(consul.ConfKeyManualPath)
		manualItems := strings.Split(strings.Trim(manualPath, "/"), "/")
		manualPath = path.Join(manualItems[:len(manualItems)-2]...)
		return manualPath
	}
}

// manualCmd represents the manual command
var manualCmd = &cobra.Command{
	Use:   "manual",
	Short: "print manual data_id info",
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()
		list, err := flags.GetBool("list")
		checkError(err, -1, "get list option failed")
		target, err := flags.GetString("target")
		checkError(err, -1, "get target failed")
		dataID, err := flags.GetString("data_id")
		checkError(err, -1, "get dataID failed")
		remove, _ := flags.GetBool("remove")

		cfg := config.Configuration
		manualPath := cfg.GetString(consul.ConfKeyManualPath)
		if !strings.HasSuffix(manualPath, "/") {
			manualPath = manualPath + "/"
		}

		conf := config.Configuration
		helper, err := scheduler.NewClusterHelper(context.Background(), conf)
		checkError(err, 1, "get consul client failed")
		passingServices, err := helper.ListServices()
		pathVersion := conf.GetString(consul.ConfKeyPathVersion)

		client, err := consul.NewConsulClient(context.Background())
		checkError(err, -1, "get consul client failed")

		if val, err := flags.GetBool("migrate"); val && err == nil {
			switch pathVersion {
			case "":
				fmt.Println("warning: ", "can't migrate manual data_ids by v0-pathVersion")
			default:
				v0Path, err := flags.GetString("origin-path")
				checkError(err, 1, "get command origin-path error")
				assumeyes, err := flags.GetBool("assumeyes")
				checkError(err, 1, "get flag assumeyes error")
				if v0Path == "" {
					v0Path = getManualV0Path(conf)
				}
				// 只取default集群的路径
				v1Path := conf.GetString(consul.ConfKeyManualPath)
				v1Path = path.Join(path.Dir(v1Path), "default")
				// xxx/v1/$cluster/data_id
				defaultDataIDPrefix, _ := parsePathByPathVersion(pathVersion, conf)
				defaultDataIDPath := path.Join(defaultDataIDPrefix, "default", "data_id")
				migrateV0ToV1Path(client, passingServices, v0Path, v1Path, defaultDataIDPath, assumeyes)
			}
		}
		if list {
			listManual(client, manualPath, pathVersion)
			return
		}

		if target == "" && dataID == "" {
			return
		}

		if remove {
			deleteManual(target, dataID, manualPath, client)
			listManual(client, manualPath, pathVersion)
			return
		}
		addManual(target, dataID, manualPath, client)
		listManual(client, manualPath, pathVersion)
	},
}

func init() {
	rootCmd.AddCommand(manualCmd)
	flags := manualCmd.Flags()
	flags.BoolP("list", "l", false, "list all manual items")
	flags.StringP("target", "t", "", "target transfer")
	flags.StringP("data_id", "d", "", "source data id")
	flags.BoolP("remove", "r", false, "remove manual item")
	flags.Bool("migrate", false, "list migrate info")
	flags.String("origin-path", "", "appoint migrate from where")
	flags.BoolP("assumeyes", "y", false, "migrate v0 manual path to v1 manual path")
}
