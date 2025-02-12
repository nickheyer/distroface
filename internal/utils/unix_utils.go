//go:build !windows

package utils

import (
	"syscall"

	"github.com/nickheyer/distroface/internal/models"
)

func GetDiskInfo(dir string) models.DiskInfo {
	var info models.DiskInfo
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		info.DiskTotal = 0
		info.DiskAvailable = 0
	} else {
		info.DiskTotal = int64(stat.Blocks) * int64(stat.Bsize)
		info.DiskAvailable = int64(stat.Bavail) * int64(stat.Bsize)
	}

	return info
}
