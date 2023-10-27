// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"io"
)

// PointsReader 针对写入性能进行优化的reader
// index 当前指针
// offset 指针子偏移量
// indexLength 指针坐标数
// allPoints 整体信息,不应被修改
// indexList 所有坐标列表
type PointsReader struct {
	index       int
	offset      int
	indexLength int
	allPoints   []byte
	indexList   []int
}

// NewPointsReader allPoints:数据指针 batchSize：当前批次最大长度,这里用于初始化indexList的大小
var NewPointsReader = func(allPoints []byte, batchSize int) CopyReader {
	return &PointsReader{0, 0, 0, allPoints, make([]int, 0, batchSize*2)}
}

// NewPointsReaderWithBytes 简单的reader
var NewPointsReaderWithBytes = func(allPoints []byte) CopyReader {
	pointsReader := &PointsReader{0, 0, 0, allPoints, make([]int, 0, 2)}
	pointsReader.AppendIndex(0, len(allPoints))
	return pointsReader
}

func (r *PointsReader) Read(b []byte) (int, error) {
	// 计算b的位置
	if r.index >= r.indexLength {
		// 序列写到最大值,返回EOF
		return 0, io.EOF
	}
	lengthB := len(b)
	startIdx := r.index
	endIdx := r.index + 1
	readLength := r.indexList[endIdx] - r.indexList[startIdx]

	// 如果刚刚传入的buffer就无法满足写入要求，则使用偏移分片输出
	// 这里只处理buffer长度比需要读取的块小的情况，当buffer超过这个块之后就走下面的逻辑进行计算
	if readLength > lengthB || r.offset != 0 {
		// 偏移量累积
		preOffset := r.offset
		r.offset = r.offset + lengthB
		startPosition := r.indexList[startIdx] + preOffset
		endPosition := r.indexList[endIdx]
		if r.indexList[endIdx]-r.indexList[startIdx] > r.offset {
			// 如果累积偏移量小于当前块，则输出单次偏移量长度
			endPosition = r.indexList[startIdx] + r.offset
		} else {
			// 如果累积偏移量大于当前块，则以当前块为准，并结束此次分片输出
			r.offset = 0
			r.index = endIdx + 1
		}
		start := startPosition
		end := endPosition
		len := end - start
		// 将指定区域的数据复制到buffer
		copy(b, r.allPoints[start:end])
		return len, nil
		// return 0, io.ErrShortBuffer
	}
	// 循环累积,如果尾部与下一位idx相等,且不越界，说明连续,尝试将尾部索引继续扩展
	for endIdx+2 < r.indexLength && r.indexList[endIdx] == r.indexList[endIdx+1] {
		// 如果继续累积会超过b的容量，则直接退出循环，直接写入当前区域的数据
		if r.indexList[endIdx+2]-r.indexList[startIdx] > lengthB {
			break
		}
		// 容错:如果idx已经超过了实际的points的大小，则输出剩余值，然后直接退出,并返回EOF提示不要再进行调用
		if r.indexList[endIdx+2] >= len(r.allPoints) {
			start := r.indexList[startIdx]
			end := len(r.allPoints)
			len := end - start
			copy(b, r.allPoints[start:end])
			// 将索引置到最后，避免再次调用read出错
			r.index = r.indexLength
			return len, io.EOF
		}
		// 否则进行累积
		endIdx = endIdx + 2
	}
	// 根据idx获取实际坐标
	start := r.indexList[startIdx]
	end := r.indexList[endIdx]
	len := end - start
	// 将指定区域的数据复制到buffer
	copy(b, r.allPoints[start:end])
	// 起始指针指向下一位
	r.index = endIdx + 1
	return len, nil
}

// Copy 获取一个副本，拥有原本的所有成员引用,但是状态位重置
func (r *PointsReader) Copy() CopyReader {
	return &PointsReader{0, 0, r.indexLength, r.allPoints, r.indexList}
}

// SeekZero 偏移量归零
func (r *PointsReader) SeekZero() {
	r.index = 0
	r.offset = 0
}

// AppendIndex 追加
func (r *PointsReader) AppendIndex(start, end int) {
	r.indexList = append(r.indexList, start, end)
	r.indexLength = r.indexLength + 2
}

// PointCount 返回当前reader下有多少个点的数据
func (r *PointsReader) PointCount() int {
	return len(r.indexList) / 2
}
