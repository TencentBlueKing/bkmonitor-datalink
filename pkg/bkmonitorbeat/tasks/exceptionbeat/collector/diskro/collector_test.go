//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package diskro

import (
	"os"
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRODiskDoesNotLeakStatusAcrossMountPoints(t *testing.T) {
	f := initStorage()
	defer os.Remove(f)

	firstHistory := NewMountPointInfo(disk.PartitionStat{
		Device:     "/dev/sdm",
		Mountpoint: "/hadoop12",
		Fstype:     "ext4",
		Opts:       []string{"rw"},
	})
	secondHistory := NewMountPointInfo(disk.PartitionStat{
		Device:     "/dev/sdb",
		Mountpoint: "/hadoop01",
		Fstype:     "ext4",
		Opts:       []string{"rw"},
	})

	require.NoError(t, firstHistory.SaveStatus())
	require.NoError(t, secondHistory.SaveStatus())
	defer cleanStorage(t, firstHistory)
	defer cleanStorage(t, secondHistory)

	originalPartitionFunc := partitionFunc
	partitionFunc = func(all bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{
			{
				Device:     "/dev/sdm",
				Mountpoint: "/hadoop12",
				Fstype:     "ext4",
				Opts:       []string{"ro"},
			},
			{
				Device:     "/dev/sdb",
				Mountpoint: "/hadoop01",
				Fstype:     "ext4",
				Opts:       []string{"rw"},
			},
		}, nil
	}
	defer func() {
		partitionFunc = originalPartitionFunc
	}()

	collector := &DiskROCollector{
		deviceMap: make(map[string]bool),
	}

	ret := collector.getRODisk()
	require.Len(t, ret, 1)
	assert.EqualValues(t, beatMapStr("/dev/sdm", "/hadoop12", "ext4"), ret[0])
}

func TestGetRODiskDoesNotLeakWhitelistReportAcrossMountPoints(t *testing.T) {
	originalPartitionFunc := partitionFunc
	partitionFunc = func(all bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{
			{
				Device:     "/dev/sdm",
				Mountpoint: "/hadoop12",
				Fstype:     "ext4",
				Opts:       []string{"ro"},
			},
			{
				Device:     "/dev/sdb",
				Mountpoint: "/hadoop01",
				Fstype:     "ext4",
				Opts:       []string{"rw"},
			},
		}, nil
	}
	defer func() {
		partitionFunc = originalPartitionFunc
	}()

	collector := &DiskROCollector{
		whiteList: []string{"hadoop12"},
		deviceMap: make(map[string]bool),
	}

	ret := collector.getRODisk()
	require.Len(t, ret, 1)
	assert.EqualValues(t, beatMapStr("/dev/sdm", "/hadoop12", "ext4"), ret[0])
}

func beatMapStr(fs, position, fileSystem string) map[string]interface{} {
	return map[string]interface{}{
		"fs":       fs,
		"position": position,
		"type":     fileSystem,
	}
}
