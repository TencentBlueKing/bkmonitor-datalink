// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris zos

package corefile

import (
	"os"
	"path"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

type PatternTestCase struct {
	pattern  string
	error    error
	expected [][]string
}

func TestCheckPattern(t *testing.T) {
	testCases := []PatternTestCase{
		{
			pattern: `corefile-%E-%e-%I-%i-%s-%t`,
			error:   nil,
			expected: [][]string{
				{"corefile-%E", "corefile-", "%E"},
				{"-%e", "-", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"%z", "", "%z"},
			},
		},
		{
			pattern: `%E-%e-%I-%i-%s-%t`,
			error:   nil,
			expected: [][]string{
				{"%E", "", "%E"},
				{"-%e", "-", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"%z", "", "%z"},
			},
		},
		{
			pattern: `corefile%E-_%e-%I-%i-%s-%t_end`,
			error:   nil,
			expected: [][]string{
				{"corefile%E", "corefile", "%E"},
				{"-_%e", "-_", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"_end%z", "_end", "%z"},
			},
		},
		{
			// 占位符之间没有分隔符
			pattern:  `%E%e-%I-%i-%s-%t`,
			error:    ErrPatternDelimiter,
			expected: nil,
		},
	}
	for _, item := range testCases {
		t.Logf("pattern: %s", item.pattern)
		c := new(CoreFileCollector)
		c.pattern = item.pattern
		err := c.checkPattern()
		assert.Equal(t, item.error, err)
		if err != nil {
			continue
		}
		for i, e := range item.expected {
			assert.Equal(t, e[0], c.patternArr[i][0])
			assert.Equal(t, e[1], c.patternArr[i][1])
			assert.Equal(t, e[2], c.patternArr[i][2])
		}
	}
}

type DimensionTestCase struct {
	pattern           string
	filePath          string
	isUsesPid         bool
	expected          map[string]string
	isAnalysisSuccess bool
}

func TestBuildDimensionKey(t *testing.T) {
	var testData = []struct {
		dimensions beat.MapStr
		result     string
	}{
		{
			dimensions: beat.MapStr{
				ExecutablePathKeyName: "/data/bin",
				ExecutableKeyName:     "bin",
				SignalKeyName:         "SIGKILL",
			},
			result: "/data/bin-bin-SIGKILL",
		},
		{
			dimensions: beat.MapStr{
				ExecutableKeyName: "bin",
				SignalKeyName:     "SIGKILL",
			},
			result: "-bin-SIGKILL",
		},
		{
			dimensions: beat.MapStr{},
			result:     "--",
		},
	}

	for _, data := range testData {
		result := buildDimensionKey(data.dimensions)
		assert.Equal(t, data.result, result)
	}
}

var (
	corePatternPathV2 = path.Join(os.TempDir(), "corefile_pattern")
)

func TestCoreFileCollectorGetCoreFilePath(t *testing.T) {
	CorePatternFile = corePatternPathV2
	err := os.WriteFile(corePatternPathV2, []byte("/data/corefile/core_%e_%t.%p\n"), 0644)
	if err != nil {
		panic(err)
	}

	testCases := []struct {
		name          string
		inputPattern  string
		resultPath    string
		resultPattern string
	}{
		{
			name:          "FileMode",
			inputPattern:  "",
			resultPath:    "/data/corefile",
			resultPattern: "core_%e_%t.%p",
		},
		{
			name:          "ConfigWithNoNewline",
			inputPattern:  "/data/corefile2/core_%e",
			resultPath:    "/data/corefile2",
			resultPattern: "core_%e",
		},
		{
			name:          "ConfigWithOneNewline",
			inputPattern:  "/data/corefile2/core_%e\n",
			resultPath:    "/data/corefile2",
			resultPattern: "core_%e",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			c := &CoreFileCollector{coreFilePattern: tt.inputPattern}
			corePath, _ := c.getCoreFilePath()
			assert.Equal(t, corePath, tt.resultPath)
			assert.Equal(t, c.pattern, tt.resultPattern)
		})
	}
}

func TestCoreFileCollector_fillDimension(t *testing.T) {
	type fields struct {
		pattern   string
		isUsesPid bool
	}
	type args struct {
		filePath string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   beat.MapStr
		reg    *regexp.Regexp
		want1  bool
	}{
		{
			name: "路径和信号",
			fields: fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-25486-8-1607502386",
			},
			want: beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			want1: true,
		},
		{
			name: "路径和信号带pid扩展名",
			fields: fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: true,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-25486-8-1607502386.25486",
			},
			want: beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			want1: true,
		},
		{
			name: "pattern和文件名不匹配",
			fields: fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!t～ded。est-tes8-1607502386.25486",
			},
			want:  beat.MapStr{},
			want1: false,
		},
		{
			name: "分隔符包含正则元字符",
			fields: fields{
				pattern:   "corefile-%E.%e\\%I~%i-%stt%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test.test\\25486~25486-8tt1607502386",
			},
			want: beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			want1: true,
		},
		{
			name: "分隔符和内容冲突-!",
			fields: fields{
				pattern:   "corefile-%E!%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test!test-25486-25486-8-1607502386",
			},
			want: beat.MapStr{
				"event_time": "1607502386",
				"signal":     "SIGFPE",
			},
			want1: true,
		},
		{
			name: "分隔符和内容冲突-8",
			fields: fields{
				pattern:   "corefile-%E-%e-%I-%i8%s-%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-2548688-1607502386",
			},
			want: beat.MapStr{
				"event_time":      "1607502386",
				"executable":      "test",
				"executable_path": "/tmp/test/test",
			},
			want1: true,
		},
		{
			name: "根据%E补充%e维度",
			fields: fields{
				pattern:   "corefile-%E-%I-%i8%s-%t",
				isUsesPid: false,
			},
			args: args{
				filePath: "/corefile/corefile-!tmp!test!test1-25486-2548688-1607502386",
			},
			want: beat.MapStr{
				"event_time":      "1607502386",
				"executable_path": "/tmp/test/test1",
				"executable":      "test1",
			},
			want1: true,
		},
		{
			name: "存在不可用的占位符",
			fields: fields{
				pattern:   "core_%w_%e",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core__demo",
			},
			want: beat.MapStr{
				"executable": "demo",
			},
			want1: true,
		},
		{
			name: "测试将pid占位符加入",
			fields: fields{
				pattern:   "core_%e_%p",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core_demo_77190",
			},
			want: beat.MapStr{
				"executable": "demo",
			},
			want1: true,
		},
		{
			name: "测试特意将pid占位符，而且存在不可用的占位符 加入到pattern中的情形",
			fields: fields{
				pattern:   "core_%w_%e_%t_%p",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core__demo_1616056187_77190",
			},
			want: beat.MapStr{
				"executable": "demo",
				"event_time": "1616056187",
			},
			want1: true,
		},
		{
			name: "设置了use_pid，但是实际上已经手动匹配过了，此时不需要划分后缀",
			fields: fields{
				pattern:   "core_%w_%e_%t_%p",
				isUsesPid: true,
			},
			args: args{
				filePath: "/data/corefile/core__demo_1616056187_77190",
			},
			want: beat.MapStr{
				"executable": "demo",
				"event_time": "1616056187",
			},
			want1: true,
		},
		{
			name: "第一个自身匹配出分隔符",
			fields: fields{
				pattern:   "core_%e_%t",
				isUsesPid: true,
			},
			args: args{
				filePath: "/data/corefile/core_gen_core_test_1668062442.10453",
			},
			want: beat.MapStr{
				"executable": "gen_core_test",
				"event_time": "1668062442",
			},
			want1: true,
		},
		{
			name: "匹配出上一个分隔符造成歧义",
			fields: fields{
				pattern:   "core_%et%h_%t",
				isUsesPid: true,
			},
			args: args{
				filePath: "/data/corefile/core_gen_test_centos_1668062442.10453",
			},
			want: beat.MapStr{
				"event_time": "1668062442",
			},
			want1: true,
		},
		{
			name: "连续三个有歧义",
			fields: fields{
				pattern:   "core_%et%ht%E_%t",
				isUsesPid: true,
			},
			args: args{
				filePath: "/data/corefile/core_gen_test_centos_test_path_1668062442.10453",
			},
			want: beat.MapStr{
				"event_time": "1668062442",
			},
			want1: true,
		},
		{
			name: "匹配出前后分隔符但和前后都不匹配",
			fields: fields{
				pattern:   "core_%t_%e_%s",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core_1668062442_gen_core_test_8",
			},
			want: beat.MapStr{
				"executable": "gen_core_test",
				"event_time": "1668062442",
				"signal":     "SIGFPE",
			},
			want1: true,
		},
		{
			name: "正则过滤(success)",
			fields: fields{
				pattern:   "core_%t_%e_%s",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core_gen_core_test",
			},
			reg:   regexp.MustCompile("gen_core_test"),
			want:  beat.MapStr{},
			want1: true,
		},
		{
			name: "正则过滤(failed)",
			fields: fields{
				pattern:   "core_%t_%e_%s",
				isUsesPid: false,
			},
			args: args{
				filePath: "/data/corefile/core_gen_core_xtest",
			},
			reg:   regexp.MustCompile("gen_core_test"),
			want:  beat.MapStr{},
			want1: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CoreFileCollector{
				pattern:   tt.fields.pattern,
				isUsesPid: tt.fields.isUsesPid,
				matchRegx: tt.reg,
			}
			err := c.checkPattern()
			assert.NoError(t, err, "checkPattern", tt.fields.pattern)
			got, got1 := c.fillDimension(tt.args.filePath)
			assert.Equalf(t, tt.want, got, "fillDimension(%v)", tt.args.filePath)
			assert.Equalf(t, tt.want1, got1, "fillDimension(%v)", tt.args.filePath)
		})
	}
}
