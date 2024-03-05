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
)

const separator = "/"

var consulClient *consulInst.Instance

// OutputDiffContent output the different content
func OutputDiffContent() {
	fmt.Println("start to diff content from src and dst ...")
	if err := util.ValidateParams(SrcPath, DstPath); err !=nil {
		fmt.Printf("validate src and dst error, %v", err)
		os.Exit(1)
	}

	consulClient = GetInstance()
	// 如果没有旁路路径，则进行全匹配
	if BypassName == "" {
		if err := output(SrcPath, DstPath); err != nil {
			fmt.Println(err)
		}
		return
	}

	// 获取全路径
	srcFullPath, dstFullPath, err := getConsulPath()
	if err !=nil {
		fmt.Printf("get consul path error, %v", err)
		os.Exit(1)
	}
	
	// 比对路径是否一致
	onlySrcPath, onlyDstPath := comparePath(&srcFullPath, &dstFullPath)
	if onlySrcPath != nil || onlyDstPath != nil {
		fmt.Printf("src path: %s, dst path: %s full path not equal\n", SrcPath, DstPath)
		fmt.Printf("only src path: %v \n", onlySrcPath)
		fmt.Printf("only dst path: %v \n", onlyDstPath)
	}else{
		fmt.Printf("src path: %s, dst path: %s count is equal\n\n", SrcPath, DstPath)
	}

	// 开始比对数据
	comparePathData(&dstFullPath)

	fmt.Println("diff successfully")
}

// get full path to get data
func getConsulPath()([]string, []string, error){
	// 获取所有全的子路径
	srcFullPath := getFullPath(SrcPath)
	if len(srcFullPath) == 0 {
		return nil, nil, fmt.Errorf("path: %s not key", SrcPath)
	}
	dstFullPath := getFullPath(DstPath)
	if len(dstFullPath) == 0 {
		return nil, nil, fmt.Errorf("path: %s not key", DstPath)
	}
	return srcFullPath, dstFullPath, nil
}

// get full path
func getFullPath(path string) []string {
	keys, _, err := consulClient.APIClient.KV().List(path, nil)
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
func comparePath(srcFullPath*[]string, dstFullPath*[]string) ([]string, []string) {
	if len(*srcFullPath) == len(*dstFullPath) {
		return nil, nil
	}
	var dstFullPathWithoutBypass []string
	for _, path := range *dstFullPath {
		dstFullPathWithoutBypass = append(dstFullPathWithoutBypass, strings.Replace(path, BypassName, "", 1))
	}
	// 比对差异, 记录仅存在于原路径中数据和旁路路径数据
	// NOTE: 仅对比剥离前缀的数据
	srcFullPathSet := slicex.StringList2Set(*srcFullPath)
	dstFullPathSet := slicex.StringList2Set(dstFullPathWithoutBypass)
	onlySrcPath := srcFullPathSet.Difference(dstFullPathSet)
	onlyDstPath := dstFullPathSet.Difference(srcFullPathSet)
	return slicex.StringSet2List(onlySrcPath), slicex.StringSet2List(onlyDstPath)
}

// compare data from path
func comparePathData(dstPathList *[]string) {
	for _, path := range *dstPathList {
		srcPath := strings.Replace(path, BypassName, "", 1)
		if err := output(srcPath, path); err != nil {
			fmt.Println(err) 
		}
	}
}

func output(srcPath string, dstPath string) error {
	srcData, _ := consulClient.Get(srcPath)
	dstData, _ := consulClient.Get(dstPath)
	srcDataJson := string(srcData)
	dstDataJson := string(dstData)
	equal, err := jsonx.CompareJson(srcDataJson, dstDataJson)
	if err != nil {
		return fmt.Errorf("path: %s compare with path: %s error, %v", srcPath, dstPath, err)
	}
	if equal {
		fmt.Printf("path: %s and path: %s is equal\n\n", srcPath, dstPath)
	} else {
		fmt.Printf("path: %s and path: %s is not equal\n", srcPath, dstPath)
		fmt.Printf("path: %s, data: %s\n", srcPath, srcDataJson)
		fmt.Printf("path: %s, data: %s\n\n", dstPath, dstDataJson)
	}
	return nil
}