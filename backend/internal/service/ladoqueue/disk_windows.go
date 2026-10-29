//go:build windows

package ladoqueue

import "golang.org/x/sys/windows"

// FreeDiskBytes reads the free disk space the daemon wires into Deps.FreeDisk.
var FreeDiskBytes = freeDiskBytes

// freeDiskBytes returns the bytes the user can still write on the volume that
// holds path.
func freeDiskBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
