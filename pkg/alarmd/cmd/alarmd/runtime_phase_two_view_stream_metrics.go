// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// viewStreamCounts is the stream's Stats in the recorder's words.
func viewStreamCounts(stats viewstream.Stats) metric.ViewStreamCounts {
	return viewStreamCountsAt(stats, time.Now())
}

func viewStreamCountsAt(stats viewstream.Stats, at time.Time) metric.ViewStreamCounts {
	counts := metric.ViewStreamCounts{
		Leading: stats.Leading, Revision: stats.Revision, Sessions: stats.Sessions,
		Expected: stats.Counts.Expected, Sent: stats.Counts.Sent, Acked: stats.Counts.Acked,
		Installed: stats.Counts.Installed, Switched: stats.Counts.Switched,
		IgnoredUnknownVersion: stats.Ignored.UnknownVersion, IgnoredUnexpectedReceiver: stats.Ignored.UnexpectedReceiver,
		IgnoredDigestMismatch: stats.Ignored.DigestMismatch, IgnoredStaleIncarnation: stats.Ignored.StaleIncarnation,
		Publications: stats.Publications, PublicationsSkipped: stats.PublicationsSkipped,
		SnapshotChunksSent: stats.SnapshotChunksSent, DeltasSent: stats.DeltasSent, EmptyDeltasSent: stats.EmptyDeltasSent,
		DeltasOversized: stats.DeltasOversized, Refusals: stats.Refusals,
		PublishFailures: stats.PublishFailuresByReason,
	}
	if !stats.PublishFailingSince.IsZero() {
		counts.PublishFailingSeconds = at.Sub(stats.PublishFailingSince).Seconds()
	}
	if !stats.NoSessionsSince.IsZero() {
		counts.NoSessionsSeconds = at.Sub(stats.NoSessionsSince).Seconds()
	}
	return counts
}

// viewClientCounts is the client's Stats in the recorder's words.
func viewClientCounts(stats viewstream.ClientStats) metric.ViewClientCounts {
	return metric.ViewClientCounts{
		Connected: stats.Connected, InstalledRevision: stats.Installed.Revision, InstalledEpoch: stats.Installed.ControlEpoch,
		ObjectsMissing: stats.ObjectsMissing, ObjectsProbed: stats.ObjectsProbed, Installs: stats.Installs, InstallFailures: stats.InstallFailures,
		SnapshotsRequested: stats.SnapshotsRequested, Refusals: stats.Refusals, Connections: stats.Connections,
		DiscoveryMisses: stats.DiscoveryMisses,
	}
}
