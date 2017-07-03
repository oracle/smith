// +build linux

package main

import "syscall"

func setAttrMappings(attr *syscall.SysProcAttr, uid int, gid int) (*syscall.SysProcAttr, error) {
	// NOTE: requires kernel that supports userns to run without root
	if uid != 0 {
		attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}}
		attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}}
		attr.Cloneflags = uintptr(syscall.CLONE_NEWUSER | syscall.SIGCHLD)
	}

	return attr, nil
}
