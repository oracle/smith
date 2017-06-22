package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"

	"github.com/oracle/smith/execute"

	"github.com/Sirupsen/logrus"
)

var (
	hasPigz *bool
)

func HasPigz() bool {
	if hasPigz == nil {
		exists := false
		if _, err := exec.LookPath("pigz"); err == nil {
			exists = true
		} else {
			logrus.Warn("pigz binary not found, falling back to internal gzip")
		}
		hasPigz = &exists
	}
	return *hasPigz
}

type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

type nopCloser struct {
	io.ReadSeeker
}

func (nopCloser) Close() error { return nil }

func NopCloser(r io.ReadSeeker) ReadSeekCloser {
	return nopCloser{r}
}

type disabledSeeker struct {
	nopCloser
}

func (disabledSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, fmt.Errorf("Seek not allowed")
}

func DisabledSeeker(r io.ReadSeeker) ReadSeekCloser {
	return disabledSeeker{nopCloser{r}}
}

func MaybeGzipReader(in ReadSeekCloser) (io.ReadCloser, error) {
	gzipReader, err := gzip.NewReader(in)
	if err != nil {
		logrus.Debugf("File is not a gzip, assuming tar: %v", err)
		in.Seek(0, 0)
		// disable seek so we get all data when calculating id
		return DisabledSeeker(in), nil
	}
	if !HasPigz() {
		return gzipReader, nil
	}
	in.Seek(0, 0)
	pigzReader, err := NewPigzReader(in)
	if err != nil {
		logrus.Errorf("Failed to read with pigz")
		return nil, err
	}
	return pigzReader, nil
}

type PigzReader struct {
	stdout      io.ReadCloser
	stdin       io.WriteCloser
	cmd         *exec.Cmd
	commandLine string
	wg          sync.WaitGroup
}

func NewPigzReader(r io.Reader) (*PigzReader, error) {
	z := PigzReader{}
	z.cmd = exec.Command("pigz", "-d")
	z.commandLine = "pigz -d"
	var err error
	z.stdout, err = z.cmd.StdoutPipe()
	if err != nil {
		logrus.Errorf("Failed to get stdout pipe for %s: %v", z.commandLine, err)
		return nil, err
	}
	z.stdin, err = z.cmd.StdinPipe()
	if err != nil {
		logrus.Errorf("Failed to get stdout pipe for %s: %v", z.commandLine, err)
		return nil, err
	}
	if err := z.cmd.Start(); err != nil {
		logrus.Errorf("Error starting %s: %v", z.commandLine, err)
		return nil, err
	}
	z.wg.Add(1)
	go func() {
		defer z.wg.Done()
		defer z.stdin.Close()
		io.Copy(z.stdin, r)
	}()
	return &z, nil
}

func (z *PigzReader) Read(p []byte) (int, error) {
	return z.stdout.Read(p)
}

func (z *PigzReader) Close() error {
	if z.cmd == nil {
		return nil
	}
	z.stdout.Close()
	z.wg.Wait()
	if err := execute.WaitExit(0, z.cmd, z.commandLine, true); err != nil {
		// ignore the sigpipe if we close the process early
		sig, ok := err.(execute.SignalExit)
		if !ok || sig.Signal != syscall.SIGPIPE {
			logrus.Error(err.Error())
			return err
		}
	}
	z.cmd = nil
	return nil
}

type PigzWriter struct {
	stdout      io.ReadCloser
	stdin       io.WriteCloser
	cmd         *exec.Cmd
	commandLine string
	wg          sync.WaitGroup
}

func MaybeGzipWriter(w io.Writer) (io.WriteCloser, error) {
	var gzipOut io.WriteCloser
	var err error
	if HasPigz() {
		gzipOut, err = NewPigzWriter(w)
		if err != nil {
			logrus.Errorf("Failed to write gzip: %v", err)
			return nil, err
		}
	} else {
		gzipOut = gzip.NewWriter(w)
	}

	return gzipOut, nil
}

func NewPigzWriter(w io.Writer) (*PigzWriter, error) {
	z := PigzWriter{}
	z.cmd = exec.Command("pigz", "-n", "-T")
	z.commandLine = "pigz -n -T"
	var err error
	z.stdout, err = z.cmd.StdoutPipe()
	if err != nil {
		logrus.Errorf("Failed to get stdout pipe for %s: %v", z.commandLine, err)
		return nil, err
	}
	z.stdin, err = z.cmd.StdinPipe()
	if err != nil {
		logrus.Errorf("Failed to get stdin pipe for %s: %v", z.commandLine, err)
		return nil, err
	}
	if err := z.cmd.Start(); err != nil {
		logrus.Errorf("Error starting %s: %v", z.commandLine, err)
		return nil, err
	}
	z.wg.Add(1)
	go func() {
		defer z.wg.Done()
		defer z.stdout.Close()
		io.Copy(w, z.stdout)
	}()
	return &z, nil
}

func (z *PigzWriter) Write(p []byte) (int, error) {
	return z.stdin.Write(p)
}

func (z *PigzWriter) Close() error {
	if z.cmd == nil {
		return nil
	}
	z.stdin.Close()
	z.wg.Wait()
	if err := execute.WaitExit(0, z.cmd, z.commandLine, true); err != nil {
		// ignore the sigpipe if we close the process early
		sig, ok := err.(execute.SignalExit)
		if !ok || sig.Signal != syscall.SIGPIPE {
			logrus.Error(err.Error())
			return err
		}
	}
	z.cmd = nil
	return nil
}
