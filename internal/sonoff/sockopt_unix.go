//go:build !windows

package sonoff

import "syscall"

func setMcastIF(fd uintptr, b [4]byte) {
	syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, b)
}
