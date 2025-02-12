//go:build windows

package utils

import (
	"github.com/nickheyer/distroface/internal/models"
	"golang.org/x/sys/windows"
)

func GetDiskInfo(dir string) models.DiskInfo {
	var info models.DiskInfo
	var (
		directoryName              = windows.StringToUTF16Ptr(dir)
		freeBytesAvailableToCaller uint64
		totalNumberOfBytes         uint64
		totalNumberOfFreeBytes     uint64
	)

	err := windows.GetDiskFreeSpaceEx(
		directoryName,
		&freeBytesAvailableToCaller,
		&totalNumberOfBytes,
		&totalNumberOfFreeBytes,
	)

	if err != nil {
		info.DiskTotal = 0
		info.DiskAvailable = 0
	} else {
		info.DiskTotal = int64(totalNumberOfBytes)
		info.DiskAvailable = int64(totalNumberOfFreeBytes)
	}

	return info
}
