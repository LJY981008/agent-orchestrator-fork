//go:build !windows

package ladoqueue

import "golang.org/x/sys/unix"

// FreeDiskBytes reads the free disk space the daemon wires into Deps.FreeDisk.
var FreeDiskBytes = freeDiskBytes

// freeDiskBytes returns the bytes an unprivileged user can still write on the
// file system that holds path.
func freeDiskBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // Bsize is positive; its type differs by OS.
}
