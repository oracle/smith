// +build linux

package execute

import (
	"syscall"
)

const termiosRead = syscall.TCGETS
