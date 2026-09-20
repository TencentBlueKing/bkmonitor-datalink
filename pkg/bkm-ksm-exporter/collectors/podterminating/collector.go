// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type Collector struct {
	state *State
	now   func() time.Time
}

func NewCollector(state *State, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{state: state, now: now}
}

func (c *Collector) Write(writer io.Writer) error {
	if c.state == nil {
		return fmt.Errorf("pod terminating state must not be nil")
	}
	snapshot := c.state.Snapshot(c.now())
	metric := func(name, help string, value float64) error {
		_, err := fmt.Fprintf(
			writer,
			"# HELP %s %s\n# TYPE %s gauge\n%s %s\n",
			name,
			help,
			name,
			name,
			strconv.FormatFloat(value, 'f', -1, 64),
		)
		return err
	}
	if err := metric(
		"pod_terminating_reporter_refresh_success",
		"Whether the latest Pod watch checkpoint and state persistence succeeded.",
		snapshot.RefreshSuccess,
	); err != nil {
		return err
	}
	if err := metric(
		"pod_terminating_reporter_last_success_timestamp_seconds",
		"Unix timestamp of the latest successful Pod watch checkpoint.",
		snapshot.LastSuccessTimestamp,
	); err != nil {
		return err
	}
	if err := metric(
		"pod_terminating_reporter_active_entries",
		"Number of persisted deleting Pod dimensions.",
		float64(snapshot.ActiveEntries),
	); err != nil {
		return err
	}
	if err := metric(
		"pod_terminating_reporter_recovery_entries",
		"Number of persisted zero-value recovery dimensions.",
		float64(snapshot.RecoveryEntries),
	); err != nil {
		return err
	}
	if err := metric(
		"pod_terminating_reporter_state_bytes",
		"Canonical serialized state size in bytes.",
		float64(snapshot.StateBytes),
	); err != nil {
		return err
	}
	if _, err := io.WriteString(
		writer,
		"# HELP pod_terminating_seconds Seconds since Pod deletion was requested.\n"+
			"# TYPE pod_terminating_seconds gauge\n",
	); err != nil {
		return err
	}
	for _, row := range snapshot.Rows {
		if _, err := fmt.Fprintf(
			writer,
			"pod_terminating_seconds{namespace=\"%s\",pod=\"%s\",node=\"%s\"} %d\n",
			escapeLabelValue(row.Namespace),
			escapeLabelValue(row.Pod),
			escapeLabelValue(row.Node),
			row.Seconds,
		); err != nil {
			return err
		}
	}
	return nil
}

func escapeLabelValue(value string) string {
	if !strings.ContainsAny(value, "\\\"\n") {
		return value
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}
