// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// AnaylizeTagData 将数据解析为格式化数据，并传回readerChannel进行处理
func AnaylizeTagData(_ uint64, readerChannel chan<- common.Points, db string, batchSize int, allPoints []byte, flowLog *logging.Entry) error {
	// 行数统计变量，处理行达到batchSize进行一次批量写入，避免单次写入量级过大
	appendCount := 0
	pos := 0
	var block []byte
	var tags Tags
	points := make(common.Points, 0, batchSize)

	// 一行一行扫描，记录每条数据的db,measurement,tags,以及index起止点
	for pos < len(allPoints) {
		startPos := pos
		pos, block = scanLine(allPoints, pos)
		pos++
		if len(block) == 0 {
			continue
		}
		blockStartPos := skipWhitespace(block, 0)

		// If line is all whitespace, just skip it
		if blockStartPos >= len(block) {
			continue
		}

		// lines which start with '#' are comments
		if block[blockStartPos] == '#' {
			continue
		}

		// strip the newline if one is present
		if block[len(block)-1] == '\n' {
			block = block[:len(block)-1]
		}
		_, key, err := scanKey(block[blockStartPos:], 0)
		if err != nil {
			flowLog.Errorf("scan key error:%s", err)
			flowLog.Warnf("scan key failed block content: [%s]", block[blockStartPos:])
			continue
		}

		// 兼容无维度情况下，获取正确的 measurement
		blockEndPos := bytes.Index(key, []byte(","))
		if blockEndPos < 0 {
			blockEndPos = len(key)
		}
		measurement := string(key[blockStartPos:blockEndPos])

		tags = parseTags(key, tags)
		// 维度为空的情况下直接返回异常
		if len(tags) == 0 {
			return errors.New(fmt.Sprintf("tags is empty with: %s", block))
		}

		pointsTags := make(common.Tags, len(tags))
		for index, tag := range tags {
			pointsTags[index] = common.Tag{
				Key:   tag.Key,
				Value: tag.Value,
			}
		}
		absoluteStartPos := startPos + blockStartPos
		point := common.Point{
			DB:          db,
			Measurement: measurement,
			Tags:        pointsTags,
			Start:       absoluteStartPos,
			End:         pos,
		}
		points = append(points, point)
		appendCount++
		if appendCount >= batchSize {
			flowLog.Tracef("batch write start")
			// 批次达到则写入
			readerChannel <- points
			points = make(common.Points, 0, batchSize)
			// 然后清空计数
			appendCount = 0
		}
	}
	// 遍历结束后要处理剩下的数据
	flowLog.Tracef("last write start")
	readerChannel <- points

	return nil
}
