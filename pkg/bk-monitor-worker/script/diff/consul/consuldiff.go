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
	"fmt"
	"os"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/script/diff/util"
	consulInst "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
	"github.com/pkg/errors"
)

const separator = "/"

var (
	srcConsulClient *consulInst.Instance
	bypassConsulClient *consulInst.Instance
	SrcPath, BypassPath string
)


// OutputDiffContent output the different content
func OutputDiffContent() {
	fmt.Println("start to diff content from src and bypass ...")
	if err := util.ValidateParams(Config.Src.Path, Config.Bypass.Path); err !=nil {
		fmt.Printf("validate src and bypass error, %v", err)
		os.Exit(1)
	}

	srcConsulClient = GetInstance(consulUtils.InstanceOptions{
		Addr:       Config.Src.Address,
		Port:       Config.Src.Port,
	})
	bypassConsulClient = GetInstance(consulUtils.InstanceOptions{
		Addr:       Config.Bypass.Address,
		Port:       Config.Bypass.Port,
	})
	SrcPath, BypassPath = Config.Src.Path, Config.Bypass.Path

	// 获取全路径
	srcFullPath, bypassFullPath, err := getConsulPath()
	if err !=nil {
		fmt.Printf("get consul path error, %v", err)
		os.Exit(1)
	}
	// 如果只有一个地址，则全路径对比
	if len(srcFullPath) == 1 && len(bypassFullPath) == 1 {
		if err := output(SrcPath, BypassPath); err != nil {
			fmt.Println(err)
		}
		return
	}
	
	// 比对路径是否一致
	onlySrcPath, onlyBypassPath := comparePath(&srcFullPath, &bypassFullPath)
	if onlySrcPath != nil || onlyBypassPath != nil {
		fmt.Printf("src path: %s, bypass path: %s full path not equal\n", SrcPath, BypassPath)
		fmt.Printf("only src path: %v \n", onlySrcPath)
		fmt.Printf("only bypass path: %v \n", onlyBypassPath)
	}else{
		fmt.Printf("src path: %s, bypass path: %s count is equal\n\n", SrcPath, BypassPath)
	}

	// 开始比对数据
	comparePathData(&bypassFullPath)

	fmt.Println("diff successfully")
}

// get full path to get data
func getConsulPath()([]string, []string, error){
	// 获取所有全的子路径
	srcFullPath := getFullPath(SrcPath, srcConsulClient)
	if len(srcFullPath) == 0 {
		return nil, nil, fmt.Errorf("path: %s not key", SrcPath)
	}
	bypassFullPath := getFullPath(BypassPath, bypassConsulClient)
	if len(bypassFullPath) == 0 {
		return nil, nil, fmt.Errorf("path: %s not key", BypassPath)
	}
	return srcFullPath, bypassFullPath, nil
}

// get full path
func getFullPath(path string, client *consulInst.Instance) []string {
	keys, _, err := client.APIClient.KV().List(path, nil)
	if err != nil {
		fmt.Printf("list path: %s keys error, %v", path, err)
		os.Exit(1)
	}
	var fullPath []string
	for _, key := range keys {
		if !strings.HasSuffix(key.Key, "/") {
			fullPath = append(fullPath, key.Key)
		}
	}
	return fullPath
}

// compare path
func comparePath(srcFullPath*[]string, bypassFullPath*[]string) ([]string, []string) {
	if len(*srcFullPath) == len(*bypassFullPath) {
		return nil, nil
	}
	var bypassFullPathWithoutBypass []string
	for _, path := range *bypassFullPath {
		bypassFullPathWithoutBypass = append(bypassFullPathWithoutBypass, strings.Replace(path, Config.Bypass.Path, Config.Src.Path, 1))
	}
	// 比对差异, 记录仅存在于原路径中数据和旁路路径数据
	// NOTE: 仅对比剥离前缀的数据
	srcFullPathSet := slicex.StringList2Set(*srcFullPath)
	bypassFullPathSet := slicex.StringList2Set(bypassFullPathWithoutBypass)
	onlySrcPath := srcFullPathSet.Difference(bypassFullPathSet)
	onlyBypassPath := bypassFullPathSet.Difference(srcFullPathSet)
	return slicex.StringSet2List(onlySrcPath), slicex.StringSet2List(onlyBypassPath)
}

// compare data from path
func comparePathData(bypassPathList *[]string) {
	for _, path := range *bypassPathList {
		srcPath := strings.Replace(path, BypassPath, SrcPath, 1)
		if err := output(srcPath, path); err != nil {
			fmt.Println(err) 
		}
	}
}

func output(srcPath string, bypassPath string) error {
	srcData, _ := srcConsulClient.Get(srcPath)
	bypassData, _ := bypassConsulClient.Get(bypassPath)
	srcDataJson := string(srcData)
	bypassDataJson := string(bypassData)
	equal, err := jsonx.CompareJson(srcDataJson, bypassDataJson)
	if err != nil {
		return errors.Errorf("path: %s compare with path: %s error, %v", srcPath, bypassPath, err)
	}
	if equal {
		fmt.Printf("path: %s and path: %s is equal\n\n", srcPath, bypassPath)
	} else {
		fmt.Printf("path: %s and path: %s is not equal\n", srcPath, bypassPath)
		fmt.Printf("path: %s, data: %s\n", srcPath, srcDataJson)
		fmt.Printf("path: %s, data: %s\n\n", bypassPath, bypassDataJson)
	}
	return nil
}