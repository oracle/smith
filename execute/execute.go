package execute

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/Sirupsen/logrus"
	"github.com/kr/pty"
)

// CloseExtraFds closes any open file descriptors other than
// stdout, stdin, and stderr. This is good hygeine before
// execing another process.
func CloseExtraFds() error {
	fdList, err := ioutil.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, fi := range fdList {
		fd, err := strconv.Atoi(fi.Name())
		if err != nil {
			continue
		}
		if fd < 3 {
			continue
		}
		syscall.CloseOnExec(fd)
	}
	return nil
}

// Exec looks up the binary in the path, closes file descriptors,
// and execs the binary.
func Exec(name string, arg ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	if err := CloseExtraFds(); err != nil {
		return err
	}
	logrus.Debugf("Execing %s %s", name, strings.Join(arg, " "))
	return syscall.Exec(path, append([]string{name}, arg...), os.Environ())
}

func Execute(name string, arg ...string) (string, string, error) {
	return execute(nil, false, 0, nil, name, arg...)
}

func AttrExecute(attr *syscall.SysProcAttr, name string, arg ...string) (string, string, error) {
	return execute(attr, false, 0, nil, name, arg...)
}

func EnvExecute(env []string, name string, arg ...string) (string, string, error) {
	return execute(nil, false, 0, env, name, arg...)
}

func TimedExecute(timeout int, env []string, name string, arg ...string) (string, string, error) {
	return execute(nil, false, timeout, env, name, arg...)
}

func ExecuteQuiet(name string, arg ...string) (string, string, error) {
	return execute(nil, true, 0, nil, name, arg...)
}

func AttrExecuteQuiet(attr *syscall.SysProcAttr, name string, arg ...string) (string, string, error) {
	return execute(attr, true, 0, nil, name, arg...)
}

func EnvExecuteQuiet(env []string, name string, arg ...string) (string, string, error) {
	return execute(nil, true, 0, env, name, arg...)
}

func TimedExecuteQuiet(timeout int, env []string, name string, arg ...string) (string, string, error) {
	return execute(nil, true, timeout, env, name, arg...)
}

// Colorizer is an io.Writer that colorizes console output.
type Colorizer struct {
	output io.Writer
	color  string
}

// Write wraps the write to the underlying io.Writer with color escape codes.
func (b *Colorizer) Write(buf []byte) (n int, err error) {
	n, err = b.output.Write(append(append([]byte(b.color), buf...), []byte("\033[0m")...))
	n -= len(b.color)
	n -= len("\033[0m")
	return n, err
}

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd uintptr) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, termiosRead, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}

// execute colorizes output in non-quiet mode and behaves like a pty.
// This means that lines are terminated with \r\n instead of just \n.
// It also will pass ctrl-c through to the executed process so that it
// can exit cleanly.
func execute(attr *syscall.SysProcAttr, quiet bool, timeout int, env []string, name string, arg ...string) (string, string, error) {
	cmd := exec.Command(name, arg...)
	cmd.Env = env
	if attr != nil {
		cmd.SysProcAttr = attr
	}
	commandLine := strings.Join(append([]string{name}, arg...), " ")

	logrus.Debugf("Executing %s", commandLine)
	var err error
	var outb, errb bytes.Buffer
	if quiet {
		cmd.Stdout = &outb
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			return "", "", fmt.Errorf("Error starting %s: %v", commandLine, err)
		}
	} else {
		if IsTerminal(os.Stdout.Fd()) {
			// fake out a tty so we get the same result as running interactively
			outpty, outtty, err := pty.Open()
			if err != nil {
				return "", "", err
			}
			defer outpty.Close()
			defer outtty.Close()
			cmd.Stdout = outtty
			go func() {
				stdoutTee := io.MultiWriter(&outb, &Colorizer{os.Stdout, "\033[35m"})
				io.Copy(stdoutTee, outpty)
			}()
		} else {
			cmd.Stdout = io.MultiWriter(&outb, os.Stdout)
		}

		if IsTerminal(os.Stderr.Fd()) {
			// fake out a tty so we get the same result as running interactively
			errpty, errtty, err := pty.Open()
			if err != nil {
				return "", "", err
			}
			defer errpty.Close()
			defer errtty.Close()
			cmd.Stderr = errtty
			go func() {
				stderrTee := io.MultiWriter(&errb, &Colorizer{os.Stderr, "\033[31m"})
				io.Copy(stderrTee, errpty)
			}()
		} else {
			cmd.Stderr = io.MultiWriter(&errb, os.Stderr)
		}
		if err := cmd.Start(); err != nil {
			return "", "", fmt.Errorf("Error starting %s: %v", commandLine, err)
		}
	}

	// wait for exit
	err = WaitExit(timeout, cmd, commandLine, quiet)
	return string(outb.Bytes()), string(errb.Bytes()), err
}

// SignalExit means the command exited via signal
type SignalExit struct {
	CommandLine string
	Signal      syscall.Signal
}

func (s SignalExit) Error() string {
	errstr := "Process %s exited with signal %d"
	return fmt.Sprintf(errstr, s.CommandLine, s.Signal)
}

// StatusExit means the command exited with a non-zero exit code
type StatusExit struct {
	CommandLine string
	Status      int
}

func (s StatusExit) Error() string {
	errstr := "Process %s exited with status %d"
	return fmt.Sprintf(errstr, s.CommandLine, s.Status)
}

// WaitExit will steal the first SIGINT, SIGTERM or SIGHUP in non-quiet mode
// and pass them through to the executed process.
func WaitExit(timeout int, cmd *exec.Cmd, commandLine string, quiet bool) error {
	sigs := make(chan os.Signal, 1)
	if !quiet {
		// go routine to pass through a sigint, sigterm, or sighup
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			sig, ok := <-sigs
			signal.Stop(sigs)
			if ok {
				cmd.Process.Signal(sig)
			}
		}()
	}

	// go routine to wait for command completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// go routine for timeout
	tchan := make(<-chan time.Time, 1)
	if timeout != 0 {
		tchan = time.After(time.Duration(timeout) * time.Millisecond)
	}
	var err error
	select {
	case <-tchan:
		logrus.Debugf("Killing process %s because it did not exit in time", commandLine)
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("Failed to kill %s: %v", commandLine, err)
		}
		<-done // allow goroutine to exit
		errstr := "Process %s timed out after %d milliseconds"
		err = fmt.Errorf(errstr, commandLine, timeout)
	case err = <-done:
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					signal := status.Signal()
					if signal != syscall.SIGTERM {
						err := SignalExit{commandLine, signal}
						logrus.Debug(err.Error())
						return err
					}
				} else {
					exitStatus := status.ExitStatus()
					if exitStatus != 0 {
						err := StatusExit{commandLine, exitStatus}
						logrus.Debug(err.Error())
						return err
					}
				}
			}
		} else if err != nil {
			logrus.Debugf("Process %s exited with error %v", commandLine, err)
		}
	}
	// clean up signal handler
	if !quiet {
		close(sigs)
	}
	return err
}
