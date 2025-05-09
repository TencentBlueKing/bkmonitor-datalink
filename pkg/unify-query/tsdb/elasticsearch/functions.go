// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	_ = 1 << (10 * iota)
	KB
	MB
	GB
)

const (
	KB_NAME = "KB"
	MB_NAME = "MB"
	GB_NAME = "GB"
)

func intMathCeil(a, b int64) int64 {
	return int64(math.Ceil(float64(a) / float64(b)))
}

func intMathFloor(a, b int64) int64 {
	if b == 0 {
		return a
	}
	return int64(math.Floor(float64(a) / float64(b)))
}

func parseSizeString(sizeStr string) (int64, error) {
	sizeStr = strings.ToUpper(sizeStr)
	var multiplier int64 = 1
	if strings.Contains(sizeStr, KB_NAME) {
		multiplier = 1024
		sizeStr = strings.ReplaceAll(sizeStr, KB_NAME, "")
	} else if strings.Contains(sizeStr, MB_NAME) {
		multiplier = 1024 * 1024
		sizeStr = strings.ReplaceAll(sizeStr, MB_NAME, "")
	} else if strings.Contains(sizeStr, GB_NAME) {
		multiplier = 1024 * 1024 * 1024
		sizeStr = strings.ReplaceAll(sizeStr, GB_NAME, "")
	}

	sizeFloat, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return 0, err
	}

	sizeBytes := int64(sizeFloat * float64(multiplier))
	return sizeBytes, nil
}

func shortDur(d time.Duration) string {
	nd := d.Milliseconds()

	if nd == 0 {
		return "0ms"
	}

	if nd%(time.Hour.Milliseconds()*24) == 0 {
		return fmt.Sprintf("%dd", nd/time.Hour.Milliseconds()/24)
	} else if nd%(time.Hour.Milliseconds()) == 0 {
		return fmt.Sprintf("%dh", nd/time.Hour.Milliseconds())
	} else if nd%(time.Minute.Milliseconds()) == 0 {
		return fmt.Sprintf("%dm", nd/time.Minute.Milliseconds())
	} else if nd%(time.Second.Milliseconds()) == 0 {
		return fmt.Sprintf("%ds", nd/time.Second.Milliseconds())
	} else {
		return fmt.Sprintf("%dms", nd)
	}
}
