//go:build windows

package utils

import (
	"github.com/nickheyer/distroface/internal/models"
)

func GetDiskInfo(dir string) models.DiskInfo {
	var info models.DiskInfo
	info.DiskTotal = 0
	info.DiskAvailable = 0
	return info
}
