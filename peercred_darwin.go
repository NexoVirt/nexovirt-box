package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// macOS is only used for development; the same check through LOCAL_PEERCRED.
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var cerr error
	err = raw.Control(func(fd uintptr) {
		var cred *unix.Xucred
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if cerr == nil {
			uid = int(cred.Uid)
		}
	})
	if err != nil {
		return -1, err
	}
	return uid, cerr
}
