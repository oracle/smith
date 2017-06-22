package main

import (
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oracle/smith/execute"

	"github.com/Sirupsen/logrus"
)

const (
	MOCK    = "/usr/bin/mock"
	SITE    = "/etc/mock/site-defaults.cfg"
	BASEDIR = "/var/lib/mock"
)

func MockBuildDebuginfo(pkgMfst *RPMManifest, mock *MockDef) (string, error) {
	config := mock.Config
	args := []string{}
	args = append(args, "-r")
	args = append(args, config)
	args = append(args, "--install")

	wantedList := pkgMfst.DebugCandidates(mock.DebugDeps)
	args = append(args, wantedList...)

	outpath := getOutPath(config)

	_, _, err := execute.Execute(MOCK, args...)
	if err != nil {
		logrus.Errorf("Failed to install debuginfo: %v", err)
		return "", err
	}

	outpath = filepath.Join(outpath, "root")
	if err = pkgMfst.FindDebugInstalled(outpath, wantedList, mock.Config); err != nil {
		return "", err
	}

	return outpath, nil
}

func MockExecuteQuiet(config, name string, arg ...string) (string, string, error) {
	args := []string{}
	args = append(args, "-r")
	args = append(args, config)
	args = append(args, "--chroot")
	args = append(args, "--")
	// combine command into single arg so shell expansion in the chroot works
	command := []string{name}
	command = append(command, arg...)
	args = append(args, strings.Join(command, " "))
	return execute.ExecuteQuiet(MOCK, args...)
}

func MockCopy(config, src, dst string) error {
	args := []string{}
	args = append(args, "-r")
	args = append(args, config)
	args = append(args, "--copyin")
	args = append(args, "--")
	args = append(args, src)
	args = append(args, dst)
	_, stderr, err := execute.ExecuteQuiet(MOCK, args...)
	if err != nil {
		logrus.Warnf("Copyin failed with stderr: %s", stderr)
	}
	return err
}

func MockBuild(name string, fast bool, mock *MockDef) (string, error) {
	config := mock.Config
	args := []string{}
	args = append(args, "-r")
	args = append(args, config)

	outpath := getOutPath(config)

	if fast {
		inst := strings.TrimSuffix(name, ".rpm")
		instArgs := append(args, "--yum-cmd", "--", "-C", "list", "installed", inst)
		_, _, err := execute.Execute(MOCK, instArgs...)
		if err == nil {
			return filepath.Join(outpath, "root"), nil
		}
	} else {
		_, _, err := execute.Execute(MOCK, append(args, "--clean")...)
		if err != nil {
			logrus.Errorf("Failed to reset build environment: %v", err)
			return "", err
		}
	}

	if mock.PreBuild != "" {
		_, _, err := execute.Execute(mock.PreBuild)
		if err != nil {
			logrus.Errorf("Failed to run pre-build: %v", err)
			return "", err
		}
	}

	if mock.PostBuild != "" {
		defer func() {
			_, _, err := execute.Execute(mock.PostBuild)
			if err != nil {
				logrus.Errorf("Failed to run post-build: %v", err)
			}
		}()
	}

	installArgs := append(args, "--install")
	installArgs = append(installArgs, mock.Deps...)
	installArgs = append(installArgs, name)
	_, _, err := execute.Execute(MOCK, installArgs...)
	if err != nil {
		logrus.Errorf("Failed to install %v: %v", name, err)
		return "", err
	}

	return filepath.Join(outpath, "root"), nil
}

func getOutPath(config string) string {
	data, err := ioutil.ReadFile(config)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^config_opts\['root'\]\s*=\s*'([^']+)'`)
	rootMatch := re.FindSubmatch(data)
	if len(rootMatch) < 2 {
		return ""
	}

	basedir := BASEDIR
	re = regexp.MustCompile(`(?m)^config_opts\['basedir'\]\s*=\s*'([^']+)'`)
	basedirMatch := re.FindSubmatch(data)
	if len(basedirMatch) >= 2 {
		basedir = string(basedirMatch[1])
	} else {
		siteData, err := ioutil.ReadFile(SITE)
		if err == nil {
			basedirMatch = re.FindSubmatch(siteData)
			if len(basedirMatch) >= 2 {
				basedir = string(basedirMatch[1])
			}
		}
	}
	return filepath.Join(basedir, string(rootMatch[1]))
}
