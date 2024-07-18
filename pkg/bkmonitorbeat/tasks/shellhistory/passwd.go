// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shellhistory

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// An Entry contains all the fields for a specific user
type Entry struct {
	User  string
	Pass  string
	Uid   int
	Gid   int
	Gecos string
	Home  string
	Shell string
}

// Parse opens the '/etc/passwd' file and parses it into a map from usernames
// to Entries
func parse() (map[int]Entry, error) {
	return parseFile("/etc/passwd")
}

// parseFile opens the file and parses it into a map from usernames to Entries
func parseFile(path string) (map[int]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer file.Close()
	return parseReader(file)
}

// parseReader consumes the contents of r and parses it into a map from
// usernames to Entries
func parseReader(r io.Reader) (map[int]Entry, error) {
	lines := bufio.NewReader(r)
	entries := make(map[int]Entry)
	for {
		line, _, err := lines.ReadLine()
		if err != nil {
			break
		}
		entry, err := parseLine(string(line))
		if err != nil {
			return nil, err
		}
		entries[entry.Uid] = *entry
	}
	return entries, nil
}

func parseLine(line string) (*Entry, error) {
	fs := strings.Split(line, ":")
	if len(fs) != 7 {
		return nil, errors.New("unexpected number of fields in /etc/passwd")
	}

	uid, err := strconv.ParseInt(fs[2], 10, 64)
	if err != nil {
		return nil, err
	}
	gid, err := strconv.ParseInt(fs[3], 10, 64)
	if err != nil {
		return nil, err
	}
	return &Entry{
		User:  fs[0],
		Pass:  fs[1],
		Uid:   int(uid),
		Gid:   int(gid),
		Gecos: fs[4],
		Home:  fs[5],
		Shell: fs[6],
	}, nil
}
