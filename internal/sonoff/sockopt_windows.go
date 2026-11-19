//go:build windows

package sonoff

import "syscall"

const ipMulticastIF = 9 // IP_MULTICAST_IF di Winsock

func setMcastIF(fd uintptr, b [4]byte) {
	syscall.SetsockoptInet4Addr(syscall.Handle(fd), syscall.IPPROTO_IP, ipMulticastIF, b)
}
