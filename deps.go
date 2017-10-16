package main

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
)

var (
	soMap        map[string]string
	preloadPaths []string
)

type executor func(name string, arg ...string) (string, string, error)

// SetSoPathsFromExecutor executes ldconfig using the given executor
// the executor should be a function which takes string args and returns
// stdout, stderr, error. The executor is responsible for setting up the
// proper environment for ldconfig, by chrooting for example.
func SetSoPathsFromExecutor(ex executor, preload []string) error {
	stdout, stderr, err := ex("ldconfig", "-v", "-N", "-X", "/")
	if err != nil {
		logrus.Warnf("ldconfig failed: %v", strings.TrimSpace(stderr))
		return nil
	}
	SetSoPaths(stdout, preload)
	return nil
}

// SetSoPaths parses a string formatted like the output from
// ldconfig -v -N -X and stores the so paths for later use by Deps
func SetSoPaths(ldconfigout string, preload []string) {
	lines := strings.Split(ldconfigout, "\n")
	soMap = map[string]string{}
	preloadPaths = preload
	path := ""
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		loc := strings.IndexRune(line, ':')
		if line[0] == '/' && loc != -1 {
			path = line[:loc]
			if strings.HasSuffix(path, ".so") {
				path = filepath.Dir(path)
			}
			continue
		}
		// lines here epresent the mapping of the generic so name to a
		// specific version <source> -> <target>
		parts := strings.Split(line, "->")
		if len(parts) > 1 {
			// we use the source name for the mapping because we want to
			// keep the symlink around for the loader
			source := strings.TrimSpace(parts[0])
			if _, ok := soMap[source]; !ok {
				soMap[source] = filepath.Join(path, source)
			}
		}
	}
}

func FindLibrary(library, chrootDir string, paths []string) string {
	for _, path := range paths {
		full := filepath.Clean(filepath.Join(path, library))
		if _, err := os.Lstat(filepath.Join(chrootDir, full)); err == nil {
			return full
		}
	}

	if soMap != nil {
		full := soMap[library]
		if full != "" {
			return full
		}
	}

	// if ldconfig didn't give us any useful files to lookup (like on alpine), we
	// may need to manually search some paths, so lets add the common ones
	for _, path := range []string{"/lib", "/usr/lib", "/usr/local/lib"} {
		full := filepath.Clean(filepath.Join(path, library))
		logrus.Debugf("Checking for %v", full)
		if _, err := os.Lstat(filepath.Join(chrootDir, full)); err == nil {
			return full
		}
	}
	return ""
}

// Deps recursively finds all statically linked dependencies of the executable
// in path within the given chroot. If nss is true, it also includes the
// relevant libnss libraries.
func Deps(chrootDir, path string, nss bool) (map[string]struct{}, error) {
	var result = map[string]struct{}{}
	elfFile, err := elf.Open(path)
	if err != nil {
		// not an elf, return empty
		logrus.Debugf("%v is not an ELF", path)
		return result, nil
	}
	defer elfFile.Close()

	needs, err := elfFile.DynString(elf.DT_NEEDED)
	if err != nil || needs == nil {
		return result, nil
	}
	paths := preloadPaths
	runpaths, err := elfFile.DynString(elf.DT_RUNPATH)
	shortPath := strings.TrimPrefix(path, chrootDir)
	origin := filepath.Dir(shortPath)
	if err == nil && runpaths != nil {
		fixed := strings.Replace(runpaths[0], "$ORIGIN", origin, -1)
		paths = append(paths, strings.Split(fixed, ":")...)
	} else {
		rpaths, err := elfFile.DynString(elf.DT_RPATH)
		if err == nil && rpaths != nil {
			fixed := strings.Replace(rpaths[0], "$ORIGIN", origin, -1)
			paths = append(paths, strings.Split(fixed, ":")...)
		}
	}

	if nss {
		for _, s := range []string{"libnss_dns.so.2", "libnss_files.so.2", "libnss_compat.so.2"} {
			full := FindLibrary(s, chrootDir, paths)
			if full != "" {
				logrus.Debugf("%v adding nss library: %v", shortPath, full)
				result[full] = struct{}{}
			}
		}
		nss = false
	}
	for _, need := range needs {
		full := FindLibrary(need, chrootDir, paths)
		if full != "" {
			logrus.Debugf("%v depends on library: %v", shortPath, full)
			result[full] = struct{}{}
			continue
		}
		logrus.Warnf("Unable to locate %s for %s", need, shortPath)
	}
	section := elfFile.Section(".interp")
	if section != nil {
		data, err := section.Data()
		if err == nil {
			interp := string(data[:len(data)-1])
			logrus.Debugf("%v uses interp: %v", shortPath, interp)
			result[interp] = struct{}{}
		}
	}
	return result, nil
}
