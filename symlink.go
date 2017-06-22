// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
)

const utf8RuneSelf = 0x80

// walkAndCopySymlinks modifes the walkSymlinks implementation to use a chroot
// when calculating absolute paths and to copy the symlinks into the output dir
func walkAndCopySymlinks(chrootDir, outputDir, path string) (string, error) {
	const maxIter = 255
	originalPath := path
	// consume path by taking each frontmost path element,
	// expanding it if it's a symlink, and appending it to b
	var b bytes.Buffer
	for n := 0; path != ""; n++ {
		if n > maxIter {
			return "", errors.New("EvalSymlinks: too many links in " + originalPath)
		}

		// find next path component, p
		var i = -1
		for j, c := range path {
			if c < utf8RuneSelf && os.IsPathSeparator(uint8(c)) {
				i = j
				break
			}
		}
		var p string
		if i == -1 {
			p, path = path, ""
		} else {
			p, path = path[:i], path[i+1:]
		}

		if p == "" {
			if b.Len() == 0 {
				// must be absolute path
				b.WriteRune(filepath.Separator)
			}
			continue
		}

		current := b.String() + p
		fi, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			b.WriteString(p)
			if path != "" || (b.Len() == 2 && len(p) == 2 && p[1] == ':') {
				b.WriteRune(filepath.Separator)
			}
			continue
		}

		// it's a symlink, put it at the front of path
		dest, err := os.Readlink(current)
		if err != nil {
			return "", err
		}

		// esnure the symlink in output directory
		logrus.Debugf("Symlink %s points to %s", current, dest)
		outpath := filepath.Join(outputDir, strings.TrimPrefix(current, chrootDir))
		logrus.Debugf("Creating %s symlink inside %s", outpath, chrootDir)
		if err := ensureSymlink(dest, outpath); err != nil {
			return "", err
		}

		if filepath.IsAbs(dest) || os.IsPathSeparator(dest[0]) {
			b.Reset()
			b.WriteString(chrootDir)
			b.WriteRune(filepath.Separator)
		}
		path = dest + string(filepath.Separator) + path
	}
	return filepath.Clean(b.String()), nil
}
