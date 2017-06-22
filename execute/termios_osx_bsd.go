// +build darwin freebsd netbsd openbsd solaris dragonfly

package execute

import (
	"syscall"
)

const termiosRead = syscall.TIOCGETA
