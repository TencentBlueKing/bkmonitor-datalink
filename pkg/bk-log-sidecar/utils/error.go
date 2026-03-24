// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package utils

import (
	"fmt"
	"os"
)

// IsNil is nil
func IsNil(err error) bool {
	return !NotNil(err)
}

// NotNil is not nil
func NotNil(err error) bool {
	return err != nil
}

// CheckError check error info
func CheckError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// CheckErrorFn check error func
func CheckErrorFn(err error, fn func(error)) {
	if err != nil {
		fn(err)
	}
}
