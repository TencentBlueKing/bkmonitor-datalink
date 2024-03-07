// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package events

import (
	"log"
	"os"
	"path"
	"time"
)

func MakeDiskSpace() {
	log.Println(time.Now(), "make diskspace")
	dirPath := os.Getenv("TEST_PATH")
	raise := os.Getenv("RAISE")
	file := path.Join(dirPath, "test.txt")
	if raise == "1" {
		// 塞满目录
		f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o755)
		if err != nil {
			log.Fatalln(err)
		}
		bs := []byte("hello world")
		for i := 0; i < 10; i++ {
			bs = append(bs, bs...)
		}
		for {
			_, err = f.Write(bs)
			if err != nil {
				log.Fatalln(err)
			}
		}
	} else {
		// 清空目录
		err := os.Remove(file)
		if err != nil {
			log.Fatalln(err)
		}
	}
}
