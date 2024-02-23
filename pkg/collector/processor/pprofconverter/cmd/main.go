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
	"fmt"
	"os"
	"time"

	"github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter/jfr"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func main() {
	dataPtr := flag.String("data", "", "data file, e.g. cortex-dev-01__kafka-0__cpu__0.jfr.gz")
	labelsPtr := flag.String("labels", "", "labels file, e.g. dump1.labels.pb.gz")
	formatPtr := flag.String("type", "", "input format, e.g. jfr")

	flag.Parse()

	if *dataPtr == "" {
		panic("data file is required")
	}
	if *formatPtr == "" {
		panic("input format is required")
	}

	var metadata = define.ProfileMetadata{
		StartTime:  time.UnixMilli(1000),
		EndTime:    time.UnixMilli(2000),
		Format:     *formatPtr,
		SampleRate: 100,
	}

	switch *formatPtr {
	case define.FormatJFR:
		data, err := jfr.ReadGzipFile(*dataPtr)
		if err != nil {
			panic(err)
		}
		labelsBytes, err := jfr.ReadGzipFile(*labelsPtr)
		if err != nil {
			logger.Errorf("failed to parse ")
		}

		jfrConverter := &jfr.Converter{}
		profiles, err := jfrConverter.ParseToPprof(
			define.ProfilesRawData{Metadata: metadata,
				Data: define.ProfileJfrFormatOrigin{Jfr: data, Labels: labelsBytes}},
		)
		fmt.Println(fmt.Sprintf("\n %d profiles converted.", len(profiles.Profiles)))
		prettyPrintProfiles(profiles.Profiles)
		writeProfilesToFile(profiles.Profiles)
	case define.FormatPprof:
		data, err := os.ReadFile(*dataPtr)
		if err != nil {
			panic(err)
		}
		pprofConverter := &pprofconverter.DefaultPprofable{}
		profiles, err := pprofConverter.ParseToPprof(
			define.ProfilesRawData{Metadata: metadata, Data: define.ProfilePprofFormatOrigin(data)})
		fmt.Println(fmt.Sprintf("\n %d profiles converted.", len(profiles.Profiles)))
		prettyPrintProfiles(profiles.Profiles)
		writeProfilesToFile(profiles.Profiles)
	default:
		panic("unsupported input type")
	}
}

func prettyPrintProfiles(profiles []*profile.Profile) {
	for _, p := range profiles {
		for _, location := range p.Location {
			fmt.Printf(
				"ID: %v Address: %v FirstLine: %d FirstFunction: %s \n",
				location.ID, location.Address, location.Line[0].Line, location.Line[0].Function.Name,
			)
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

	if err := os.WriteFile("profiles.pb.gz", data, 0644); err != nil {
		panic(err)
	}
	fmt.Println("writing profiles to file finished -> profiles.pb.gz")
}
