// +build !linux

package main

import (
	"errors"
	"syscall"
)

func setAttrMappings(attr *syscall.SysProcAttr, uid int, gid int) (*syscall.SysProcAttr, error) {
	return attr, errors.New("You are running on an supported OS. Please use Linux")
}
