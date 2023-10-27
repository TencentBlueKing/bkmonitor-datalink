// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"testing"
	"unicode"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
)

type TestReaderSuite struct {
	suite.Suite
}

func TestReaderRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
}

func (t *TestSuite) TestRead() {
	rd := backend.NewPointsReader([]byte("012345678"), 2)
	rd.AppendIndex(0, 2)  // 0123
	rd.AppendIndex(2, 4)  // 23
	rd.AppendIndex(4, 6)  // 45
	rd.AppendIndex(6, 10) // 678
	b := make([]byte, 20)
	i, err := rd.Read(b)
	t.Equal(9, i)
	t.Equal(io.EOF, err)
	str := make([]byte, 0)
	for _, v := range b {
		if v != 0 {
			str = append(str, v)
		}
	}
	t.Equal("012345678", string(str))
}

func (t *TestSuite) TestReadAll() {
	rd := backend.NewPointsReader([]byte("012345678"), 2)
	rd.AppendIndex(0, 4) // 0123
	rd.AppendIndex(6, 9) // 678
	b := make([]byte, 2)
	b, err := ioutil.ReadAll(rd)
	i := len(b)
	t.Equal(7, i)
	t.Nil(err)

	str := make([]byte, 0)
	for _, v := range b {
		if v != 0 {
			str = append(str, v)
		}
	}
	t.Equal("0123678", string(str))
}

func (t *TestSuite) TestShortRead() {
	rd := backend.NewPointsReader([]byte("0123456789"), 2)
	rd.AppendIndex(0, 4) // 0123
	// rd.AppendIndex(3, 4)  //3
	rd.AppendIndex(4, 6)  // 45
	rd.AppendIndex(8, 10) // 89
	str := make([]byte, 0)
	for {
		b := make([]byte, 2)
		_, err := rd.Read(b)
		if err == io.EOF {
			break
		}
		str = append(str, b...)

	}
	t.Equal("01234589", string(str))
}

func (t *TestSuite) TestReread() {
	rd := backend.NewPointsReaderWithBytes([]byte("0123456789"))
	buf1, err := ioutil.ReadAll(rd)
	t.Nil(err)
	t.Equal("0123456789", string(buf1))
	rd.SeekZero()
	buf2, err := ioutil.ReadAll(rd)
	t.Nil(err)
	t.Equal("0123456789", string(buf2))
	rd.SeekZero()
	buf3, err := ioutil.ReadAll(rd)
	t.Nil(err)
	t.Equal("0123456789", string(buf3))
}

func (t *TestSuite) TestReadRealData() {
	allPoints, err := ioutil.ReadFile("../testdata/data/test_request.txt")
	t.Nil(err)
	// 按表分割,批次写入
	pointsTable := make(map[string]backend.CopyReader)

	// 如果末尾没有\n,就增加一个
	if !bytes.HasSuffix(allPoints, []byte("\n")) {
		allPoints = append(allPoints, []byte("\n")...)
	}
	// 寻找\n获取第一条数据
	idx := bytes.Index(allPoints[:], []byte("\n"))

	// 记录当前的起始位置
	start := 0
	appendCount := 0
	batchSize := 5000
	allByteList := make([][]byte, 0)
	// 开始处理
	for idx != -1 {
		end := start + idx + 1
		comma := bytes.Index(allPoints[start:], []byte(","))
		table := bytes.TrimLeftFunc(allPoints[start:start+comma], unicode.IsSpace)
		if reader, ok := pointsTable[string(table)]; ok {
			reader.AppendIndex(start, end)
		} else {
			// 新建对应的reader
			reader = backend.NewPointsReader(allPoints, batchSize)
			reader.AppendIndex(start, end)
			pointsTable[string(table)] = reader
		}
		appendCount++
		if appendCount >= batchSize {
			// 批次达到则写入
			for tableName, reader := range pointsTable {
				t.Equal("proc", tableName)
				buf, err := ioutil.ReadAll(reader)
				t.Nil(err)
				allByteList = append(allByteList, buf)
			}

			// 写入结束后清空pointsTable
			pointsTable = make(map[string]backend.CopyReader)
			// 然后清空计数
			appendCount = 0
		}

		start = end
		idx = bytes.Index(allPoints[start:], []byte("\n"))
	}
	// 遍历结束后要处理剩下的数据
	if len(pointsTable) > 0 {
		for tableName, reader := range pointsTable {
			t.Equal("proc", tableName)
			buf, err := ioutil.ReadAll(reader)
			t.Nil(err)
			allByteList = append(allByteList, buf)
		}
	}
	expect := string(allPoints)
	actual := string(bytes.Join(allByteList, []byte("")))
	t.Equal(expect, actual)
}

func (t *TestSuite) TestBufferChange() {
	// 测试多次读取，给read方法提供的缓冲区不断变化是否会导致读取错位
	lengthList := []int{2, 4, 8}
	allpoints := "012345678901234567890123456789"
	rd := backend.NewPointsReader([]byte(allpoints), 2)
	rd.AppendIndex(0, 4)   // 0123
	rd.AppendIndex(4, 8)   // 4567
	rd.AppendIndex(8, 10)  // 89
	rd.AppendIndex(10, 11) // 0
	rd.AppendIndex(11, 12) // 1
	rd.AppendIndex(12, 15) // 234
	rd.AppendIndex(15, 19) // 5678
	rd.AppendIndex(19, 20) // 9
	rd.AppendIndex(20, 30) // 0123456789
	counter := 0
	idx := 0
	str := make([]byte, 0)
	for {
		batch := counter % 3
		counter++
		b := make([]byte, lengthList[batch])
		num, err := rd.Read(b)
		t.Equal(string(allpoints[idx:idx+num]), string(b[:num]))
		str = append(str, b[:num]...)
		idx = idx + num
		if err == io.EOF {
			break
		}
	}
	t.Equal("012345678901234567890123456789", string(str))
}
