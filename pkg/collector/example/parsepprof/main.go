// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"time"

	"github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pproftranslator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pproftranslator/jfr"
)

// Profile 命令行工具，用来解析导出的 Pprof 格式、Jfr 格式的 Profile 数据

func main() {
	dataFile := flag.String("data", "", "data file, e.g. cortex-dev-01__kafka-0__cpu__0.jfr.gz")
	labelsFile := flag.String("labels", "", "labels file, e.g. dump1.labels.pb.gz")
	inputFormat := flag.String("type", "", "input format, e.g. jfr")

	flag.Parse()

	if *dataFile == "" {
		panic("data file is required")
	}
	if *inputFormat == "" {
		panic("input format is required")
	}

	metadata := define.ProfileMetadata{
		StartTime:  time.UnixMilli(1000),
		EndTime:    time.UnixMilli(2000),
		Format:     *inputFormat,
		SampleRate: 100,
	}

	switch *inputFormat {
	case define.FormatJFR:
		data, err := jfr.ReadGzipFile(*dataFile)
		if err != nil {
			log.Fatalf("read data file failed: %v", err)
		}
		labelsBytes, err := jfr.ReadGzipFile(*labelsFile)
		if err != nil {
			log.Fatalf("read labels file failed: %v", err)
		}

		translator := &jfr.Translator{}
		profiles, err := translator.Translate(
			define.ProfilesRawData{
				Metadata: metadata,
				Data:     define.ProfileJfrFormatOrigin{Jfr: data, Labels: labelsBytes},
			},
		)
		if err != nil {
			log.Fatalf("translate failed, err: %v", err)
		}

		log.Printf("%d profiles converted.\n", len(profiles.Profiles))
		prettyPrintProfiles(profiles.Profiles)
		writeProfilesToFile(profiles.Profiles)

	case define.FormatPprof:
		data, err := os.ReadFile(*dataFile)
		if err != nil {
			log.Fatalf("read data file failed: %v", err)
		}

		translator := pproftranslator.NewTranslator(pproftranslator.Config{})
		profiles, err := translator.Translate(
			define.ProfilesRawData{
				Metadata: metadata,
				Data:     define.ProfilePprofFormatOrigin(data),
			},
		)
		if err != nil {
			log.Fatalf("translate failed, err: %v", err)
		}

		log.Printf("%d profiles converted.\n", len(profiles.Profiles))
		prettyPrintProfiles(profiles.Profiles)
		writeProfilesToFile(profiles.Profiles)

	default:
		panic("unsupported input type")
	}
}

func prettyPrintProfiles(profiles []*profile.Profile) {
	for _, p := range profiles {
		for _, location := range p.Location {
			log.Printf("ID: %v Address: %v FirstLine: %d FirstFunction: %s\n",
				location.ID, location.Address, location.Line[0].Line, location.Line[0].Function.Name)
		}
	}
}

func writeProfilesToFile(profiles []*profile.Profile) {
	var data []byte
	for _, p := range profiles {
		var protoBuf bytes.Buffer
		if err := p.WriteUncompressed(&protoBuf); err != nil {
			panic(err)
		}
		data = append(data, protoBuf.Bytes()...)
	}

	if err := os.WriteFile("profiles.pb.gz", data, 0o644); err != nil {
		panic(err)
	}
	log.Println("writing profiles to file finished -> profiles.pb.gz")
}
