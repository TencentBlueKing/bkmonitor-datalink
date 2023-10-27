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
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type outputData struct {
	Data   []string      `json:"data"`
	Result []string      `json:"result"`
	Name   string        `json:"name"`
	Time   time.Duration `json:"time"`
	Count  int           `json:"count"`
	Error  error         `json:"error"`
}

func splitInput(v string) (string, interface{}) {
	var res interface{}
	value := strings.Split(v, ":")
	strSlice := make([]string, 0)
	for _, value := range value[1:] {
		strSlice = append(strSlice, value)
	}
	resValue := strings.Join(strSlice, ":")
	if err := json.Unmarshal([]byte(resValue), &res); err != nil {
		return value[0], resValue
	}
	return value[0], res
}

func parseStringArrayToConfig(array []string, configuration interface{}) {
	conf := config.NewConfiguration()
	for _, value := range array {
		k, v := splitInput(value)
		conf.Set(k, v)
	}
	utils.CheckError(conf.Unmarshal(configuration))
}

func parseStringArrayToFieldList(array []string, rt *config.MetaResultTableConfig) {
	conf := config.NewConfiguration()
	field := map[string]config.MetaFieldConfig{}
	fieldList := make([]*config.MetaFieldConfig, 0)
	for _, value := range array {
		k, v := splitInput(value)
		conf.Set(k, v)
	}
	utils.CheckError(conf.Unmarshal(&field))
	for key, value := range field {
		f := value
		f.FieldName = key
		fieldList = append(fieldList, &f)
	}
	rt.FieldList = fieldList
}

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:     "test",
	Short:   "Test data by data processor",
	Example: `./transfer-dev test -f testm.tag:"metric" _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.es_type:'"keyword"' --pipeline option.group_info_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gs eIndex","_iteration_idx"]' --table schema_type:'"free"' -n flat.batch -T 10s`,
	Run: func(cmd *cobra.Command, args []string) {
		var (
			flags       = cmd.Flags()
			ctx         = context.Background()
			payload     define.Payload
			KillChan    = make(chan error)
			outputCh    = make(chan define.Payload)
			countTime   time.Duration
			err         error
			pipeline    = config.NewPipelineConfig()
			resultTable = config.MetaResultTableConfig{}
			inputFile   *os.File
			outputFile  *os.File
			outputData  = outputData{}
			wg          sync.WaitGroup
			lastLine    []byte
		)
		name, err := flags.GetString("name")
		utils.CheckError(err)
		input, err := flags.GetString("input")
		utils.CheckError(err)
		pipe, err := flags.GetStringArray("pipeline")
		utils.CheckError(err)
		rt, err := flags.GetStringArray("table")
		utils.CheckError(err)
		field, err := flags.GetStringArray("field")
		utils.CheckError(err)
		timeout, err := flags.GetDuration("timeout")
		utils.CheckError(err)
		payloadType, err := flags.GetString("payload")
		utils.CheckError(err)
		output, err := flags.GetString("output")
		utils.CheckError(err)

		ctx, cancel := context.WithTimeout(ctx, timeout)
		parseStringArrayToConfig(pipe, pipeline)
		ctx = config.PipelineConfigIntoContext(ctx, pipeline)
		parseStringArrayToConfig(rt, &resultTable)
		ctx = config.ResultTableConfigIntoContext(ctx, &resultTable)
		parseStringArrayToFieldList(field, &resultTable)
		pipeline.ResultTableList = append(pipeline.ResultTableList, &resultTable)
		// 使用真实pipe config
		raw, err := flags.GetString("raw")
		utils.CheckError(err)
		if raw != "" {
			utils.CheckError(json.Unmarshal([]byte(raw), pipeline))
			ctx = config.PipelineConfigIntoContext(ctx, pipeline)
			ctx = config.ResultTableConfigIntoContext(ctx, pipeline.ResultTableList[0])
		}
		// 打印config
		detail, err := flags.GetBool("verbose")
		utils.CheckError(err)
		if detail {
			v, err := json.Marshal(pipeline)
			utils.CheckError(err)
			logging.Infof("pipeline config: %+v", conv.String(v))
		}
		// 输入
		if input == "" {
			inputFile = os.Stdin
		} else {
			inputFile, err = os.Open(input)
			if err != nil {
				panic(err)
			}
			defer func() {
				utils.CheckError(inputFile.Close())
			}()
		}

		if output == "" {
			outputFile = os.Stdout
		} else {
			outputFile, err = os.OpenFile(output, os.O_RDWR|os.O_APPEND, 0o777)
			if err != nil {
				panic(err)
			}
			defer func() {
				utils.CheckError(outputFile.Close())
			}()
		}

		reader := bufio.NewReader(inputFile)
		processor, err := define.NewDataProcessor(ctx, name)
		if err != nil {
			panic(err)
		}
		i := 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			for value := range outputCh {
				var str []byte
				utils.CheckError(value.To(&str))
				start := time.Now()
				outputData.Result = append(outputData.Result, conv.String(str))
				countTime += time.Since(start)
			}
		}()
		for {
			line, isPrefix, err := reader.ReadLine()
			if err != nil {
				if err == io.EOF {
					break
				} else {
					panic(err)
				}
			}

			// 此时表示一次性读取所有的数据不可行，需要下一次一起合并line
			if isPrefix {
				// 判断是否之前已经有过一次prefix的缓存，如果是，需要将本次的信息和上次的一起合并
				if lastLine != nil {
					lastLine = append(lastLine, line...)
				} else {
					// 创建一个新的缓存区，将内容复制到新的缓存中，否则下次读取会直接覆盖当前line缓冲区
					lastLine = make([]byte, len(line))
					copy(lastLine, line)
				}
				continue
			}

			// 此时已经没有prefix了，需要检查是否有之前的内容需要合并处理
			if lastLine != nil {
				line = append(lastLine, line...)
				lastLine = nil
			}

			outputData.Data = append(outputData.Data, conv.String(line))
			switch payloadType {
			default:
				payload = define.NewJSONPayloadFrom([]byte(line), i)
			}
			processor.Process(payload, outputCh, KillChan)
			i++
		}
		close(outputCh)
		wg.Wait()
		cancel()

		// 打印时间
		logging.Infof("total time: %v", countTime)
		outputData.Name = name
		outputData.Error = err
		outputData.Count = len(outputData.Result)
		outputData.Time = countTime
		// 输出
		v, err := json.Marshal(outputData)
		_, err = io.WriteString(outputFile, conv.String(v)+"\n")
		utils.CheckError(err)
		if output != "" {
			logging.Infof("write %v successfully", output)
		}
	},
}

func init() {
	rootCmd.AddCommand(testCmd)
	flags := testCmd.Flags()
	flags.StringP("input", "i", "", "input data path")
	flags.StringP("output", "o", "", "output data path")
	flags.StringP("name", "n", "", "data processor name")
	flags.StringP("payload", "P", "json", "payload format")
	flags.DurationP("timeout", "T", 50*time.Second, "timeout")
	// flags.IntP("num", "", 1, "run time") 随后支持运行次数
	flags.StringArrayP("field", "f", []string{}, "field config")
	flags.StringArrayP("pipeline", "p", []string{}, "pipeline config")
	flags.StringArrayP("table", "t", []string{}, "result table config")
	flags.BoolP("verbose", "v", false, "show field list")
	flags.StringP("level", "l", "info", "log level")
	flags.StringP("raw", "r", "", "null pipeline config with resultTable")
}
