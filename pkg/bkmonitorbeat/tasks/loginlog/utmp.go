// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package loginlog

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"time"
)

// man utmp
//
// #define UT_LINESIZE 12
// #define UT_NAMESIZE 32
// #define UT_HOSTSIZE 256
//
// struct exit_status {
//  short int e_termination; /* process termination status. */
//  short int e_exit; /* process exit status. */
// };
//
// struct utmp {
//  short ut_type; /* type of login */
//  pid_t ut_pid; /* pid of login process */
//  char ut_line[UT_LINESIZE]; /* device name of tty - "/dev/" */
//  char ut_id[4]; /* init id or abbrev. ttyname */
//  char ut_user[UT_NAMESIZE]; /* user name */
//  char ut_host[UT_HOSTSIZE]; /* hostname for remote login */
//  struct exit_status ut_exit; /* The exit status of a process
//  marked as DEAD_PROCESS. */
//  long ut_session; /* session ID, used for windowing*/
//  struct timeval ut_tv; /* time entry was made. */
//  int32_t ut_addr_v6[4]; /* IP address of remote host. */
//  char pad[20]; /* Reserved for future use. */
// };

const (
	sizeLine = 32
	sizeName = 32
	sizeHost = 256
)

type ExitStatus struct {
	Termination int16
	Exit        int16
}

type TimeVal struct {
	Sec  int32
	Usec int32
}

type Utmp struct {
	Type     int16
	_        [2]byte // alignment
	Pid      int32
	Device   [sizeLine]byte
	Id       [4]byte
	User     [sizeName]byte
	Host     [sizeHost]byte
	Exit     ExitStatus
	Session  int32
	Time     TimeVal
	AddrV6   [16]byte
	Reserved [20]byte
}

type UtmpEntity struct {
	Type   int    `json:"type"`
	Device string `json:"device"`
	Id     string `json:"id"`
	User   string `json:"user"`
	Host   string `json:"host"`
	Time   int64  `json:"time"`
	Addr   string `json:"addr"`
}

func getByteLen(b []byte) int {
	n := bytes.IndexByte(b[:], 0)
	if n == -1 {
		return 0
	}

	return n
}

func AsUtmpEntity(u *Utmp) UtmpEntity {
	entity := UtmpEntity{
		Type:   int(u.Type),
		Device: string(u.Device[:getByteLen(u.Device[:])]),
		Id:     string(u.Id[:getByteLen(u.Id[:])]),
		User:   string(u.User[:getByteLen(u.User[:])]),
		Host:   string(u.Host[:getByteLen(u.Host[:])]),
		Time:   time.Unix(int64(u.Time.Sec), 0).Unix(),
		Addr:   u.Addr().String(),
	}
	return entity
}

func (r *Utmp) Addr() net.IP {
	ip := make(net.IP, 16)
	binary.Read(bytes.NewReader(r.AddrV6[:]), binary.BigEndian, ip)
	if bytes.Equal(ip[4:], net.IPv6zero[4:]) {
		ip = ip[:4]
	}
	return ip
}

func Unpack(r io.Reader) ([]UtmpEntity, error) {
	var us []UtmpEntity

	for {
		u, err := readLine(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		us = append(us, AsUtmpEntity(u))
	}

	return us, nil
}

func readLine(r io.Reader) (*Utmp, error) {
	u := new(Utmp)
	err := binary.Read(r, binary.LittleEndian, u)
	if err != nil {
		return nil, err
	}

	return u, nil
}
