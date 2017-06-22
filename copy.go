package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
)

func CopyTree(baseDir, outputDir string, globs []string, excludes []string, nss, follow, chroot bool) error {
	dir, err := os.Getwd()
	if err != nil {
		logrus.Errorf("Failed to get working directory: %v", err)
		return err
	}
	err = os.Chdir(baseDir)
	if err != nil {
		logrus.Errorf("Failed to change directory to %v: %v", baseDir, err)
		return err
	}
	defer os.Chdir(dir)

	chrootDir := ""
	if chroot == true {
		chrootDir = baseDir
	}

	exs := map[string]struct{}{}
	for _, glob := range excludes {
		// ignore empty globs
		if glob == "" {
			continue
		}
		// glob absolute paths as relative from current directory
		if filepath.IsAbs(glob) {
			glob = filepath.Clean(glob)[1:]
		}
		paths, err := filepath.Glob(glob)
		if err == filepath.ErrBadPattern {
			logrus.Errorf("Illegal filepath pattern: %v", glob)
			return err
		}
		for _, path := range paths {
			exs[path] = struct{}{}
		}
	}

	if len(globs) == 0 {
		globs = []string{"*"}
	}

	for _, glob := range globs {
		// ignore empty globs
		if glob == "" {
			continue
		}
		// glob absolute paths as relative from current directory
		if filepath.IsAbs(glob) {
			glob = filepath.Clean(glob)[1:]
		}
		paths, err := filepath.Glob(glob)
		if err == filepath.ErrBadPattern {
			logrus.Errorf("Illegal filepath pattern: %v", glob)
			return err
		}
		for _, path := range paths {
			// pass excludes to walk so that subdirectories can be excluded
			err = filepath.Walk(path, copier(exs, chrootDir, outputDir, nss, follow))
			if err != nil {
				logrus.Errorf("Failed to walk %v: %v", path, err)
				return err
			}
		}
	}
	return nil
}

func Copy(src string, dst string) error {
	// try hard linking first before falling back to a copy
	if err := os.Link(src, dst); err == nil {
		return nil
	}

	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return nil
}

func Rchown(path string, uid, gid int) error {
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, uid, gid); !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	if err != nil {
		logrus.Errorf("Failed to walk %v: %v", path, err)
		return err
	}
	return nil
}

func ensureSymlink(dest, source string) error {
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		logrus.Errorf("Failed to create directories for %v: %v", source, err)
		return err
	}
	err := os.Symlink(dest, source)
	if err != nil {
		if !os.IsExist(err) {
			logrus.Errorf("Failed to create symlink from %v to %v", source, dest)
			return err
		}
		sd, nerr := os.Lstat(source)
		if nerr != nil {
			logrus.Errorf("Failed to stat %v: %v", source, nerr)
			return nerr
		}
		if sd.Mode()&os.ModeSymlink == 0 {
			logrus.Errorf("Failed to create symlink from %v to %v", source, dest)
			return err
		}
		ndest, nerr := os.Readlink(source)
		if nerr != nil {
			logrus.Errorf("Failed to read symlink %v: %v", source, nerr)
			return nerr
		}
		if ndest != dest {
			logrus.Errorf("Symlink %v already exists, but it points to %v instead of %v", source, ndest, dest)
			return err
		}
	} else {
		logrus.Debugf("Symlink created from %v to %v", source, dest)
	}
	return nil
}

func copier(exs map[string]struct{}, chrootDir, outputDir string, nss, follow bool) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// if we are follow, we may find the file through walkAndCopy
			if !follow || !os.IsNotExist(err) {
				return err
			}
		}
		rel := strings.TrimPrefix(path, chrootDir)
		if _, ok := exs[rel]; ok {
			return nil
		}
		if follow {
			// NOTE: directory symlinks will not be excluded by excludes
			nPath, err := walkAndCopySymlinks(chrootDir, outputDir, path)
			if err != nil {
				if os.IsNotExist(err) {
					logrus.Debugf("Skipping dangling link for %v", path)
					return nil
				}
				logrus.Errorf("Failed to walk symlinks for %v: %v", path, err)
				return err
			}
			if path != nPath {
				rel = strings.TrimPrefix(nPath, chrootDir)
				if _, ok := exs[rel]; ok {
					return nil
				}
				path = nPath
				info, err = os.Lstat(path)
				if err != nil {
					logrus.Errorf("Failed to lstat %v: %v", path, err)
					return err
				}
			}
		}
		outpath := filepath.Join(outputDir, rel)
		if _, err := os.Stat(outpath); err == nil {
			// if follow is false, overwrite the file at location
			if follow || info.IsDir() {
				logrus.Debugf("Path %v already exists", outpath)
				return nil
			} else {
				logrus.Debugf("Overwriting %v", outpath)
				os.RemoveAll(outpath)
			}
		}
		base := outpath
		if !info.IsDir() {
			base = filepath.Dir(outpath)
		}
		if err := os.MkdirAll(base, 0755); err != nil {
			logrus.Errorf("Failed to create directories %v: %v", base, err)
			return err
		}
		if info.IsDir() {
			return nil
		}
		// only happens if !follow
		if info.Mode()&os.ModeSymlink != 0 {
			dest, err := os.Readlink(path)
			if err != nil {
				logrus.Errorf("Symlink cannot be read: %v", err)
				return err
			}
			// copy the symlink
			if err := ensureSymlink(dest, outpath); err != nil {
				return err
			}
		} else {
			logrus.Debugf("Copying file from: %v to %v", path, outpath)
			// copy the file
			err = Copy(path, outpath)
			if err != nil {
				logrus.Errorf("Failed to copy file: %v", err)
				return err
			}
			if info.Mode()&0100 != 0 {
				// executable
				os.Chmod(outpath, 0755)
				if follow {
					deps, err := Deps(chrootDir, path, nss)
					if err != nil {
						return err
					}
					for dep := range deps {
						logrus.Debugf("Walking dependency: %v", dep)

						if filepath.IsAbs(dep) {
							dep = filepath.Join(chrootDir, dep)
						} else {
							dep, err = filepath.Abs(filepath.Join(filepath.Dir(path), dep))
							if err != nil {
								logrus.Errorf("Could not determine path of dep: %v", err)
								return err
							}
						}
						filepath.Walk(dep, copier(exs, chrootDir, outputDir, nss, follow))
					}
				}
			}
		}
		return nil
	}
}
