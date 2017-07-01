package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oracle/smith/execute"

	"github.com/Sirupsen/logrus"
)

const (
	rootfs = "rootfs"
)

type buildOptions struct {
	insecure bool
	fast     bool
	conf     string
	dir      string
	buildNo  string
}

func isOci(uri string) bool {
	// urls are oci images
	if strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://") {
		return true
	}
	// split off potential tag from uri
	parts := strings.Split(uri, ":")
	file := parts[0]
	if len(parts) > 2 {
		file = parts[len(parts)-2]
	}
	// if the filename is a tar ot tgz assume oci
	if strings.HasSuffix(file, ".tar") ||
		strings.HasSuffix(file, ".tar.gz") ||
		strings.HasSuffix(file, ".tgz") {
		return true
	}
	return false
}

// installPackage returns a list of all packages installed if applicable
func installPackage(buildOpts *buildOptions, outputDir string, pkg *ConfigDef) ([]string, error) {
	logrus.Infof("Installing package %v", pkg.Package)
	if pkg.Type == "" {
		if isOci(pkg.Package) {
			pkg.Type = "oci"
		} else {
			pkg.Type = "mock"
		}
	}
	switch pkg.Type {
	case "mock":
		if pkg.Mock.Config == "" {
			pkg.Mock.Config = "/etc/mock/default.cfg"
		}
		pkgMfst := NewRPMManifest()
		if err := buildMock(buildOpts, outputDir, pkg, pkgMfst); err != nil {
			return nil, err
		}
		packages := []string{}
		for key := range pkgMfst.PkgsInstalled {
			packages = append(packages, key)
		}
		sort.Strings(packages)
		return packages, nil
	case "oci":
		if err := buildOci(buildOpts, outputDir, pkg); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("Package type %v not recognized", pkg.Type)
	}
}

func getMetadata() *ImageMetadata {
	return &ImageMetadata{
		SmithVer:  ver,
		SmithSha:  sha,
		BuildTime: time.Now().UTC(),
	}
}

func buildContainer(tarfile string, buildOpts *buildOptions) bool {
	outpath, err := filepath.Abs(tarfile)
	if err != nil {
		logrus.Infof("Failed to get abs path of %v: %v", tarfile, err)
		return false
	}

	current, err := os.Getwd()
	if err != nil {
		logrus.Infof("Failed to get working directory: %v", err)
		return false
	}

	path := current
	if buildOpts.dir != "" {
		path, err = filepath.Abs(buildOpts.dir)
		if err != nil {
			logrus.Infof("Failed to get abs path of %v: %v", buildOpts.dir, err)
			return false
		}
		err = os.Chdir(path)
		if err != nil {
			logrus.Infof("Failed to change directory to %v: %v", path, err)
			return false
		}
		defer os.Chdir(current)
	}

	pkg, err := ReadConfig(buildOpts.conf)
	if err != nil {
		logrus.Infof("Failed to read config: %v", err)
		return false
	}

	buildDir, err := ioutil.TempDir("", "smith-build-")
	if err != nil {
		logrus.Infof("Unable to get temp dir: %v", err)
		return false
	}
	defer func() {
		// remove directory
		logrus.Infof("Removing %v", buildDir)
		err = os.RemoveAll(buildDir)
		if err != nil {
			logrus.Infof("Failed to remove %v: %v", buildDir, err)
		}
	}()

	if err := os.Chmod(buildDir, 0777); err != nil {
		logrus.Infof("Failed to make %v writeable: %v", buildDir, err)
		return false
	}
	logrus.Infof("Building in %v", buildDir)

	// build package
	logrus.Infof("Installing package")
	outputDir, err := rootfsDir(buildDir, rootfs)
	if err != nil {
		logrus.Infof("Failed to get rootfs dir: %v", err)
		return false
	}

	packages, err := installPackage(buildOpts, outputDir, pkg)
	if err != nil {
		logrus.Infof("Failed to install %v: %v", pkg.Package, err)
		return false
	}

	if pkg.Nss {
		if pkg.User == "" {
			pkg.User = "smith"
		}
		logrus.Infof("Adding user %s", pkg.User)
		if err := Users(outputDir, []string{pkg.User}); err != nil {
			logrus.Infof("Failed to create users: %v", err)
			return false
		}
	}

	for _, mnt := range pkg.Mounts {
		err = os.MkdirAll(filepath.Join(outputDir, mnt), 0755)
		if err != nil {
			logrus.Infof("Failed to create %v dir: %v", mnt, err)
			return false
		}
	}

	// ensure meta directory
	// TODO: what do we do with meta?
	metaDir := filepath.Join(buildDir, ".meta")
	err = os.MkdirAll(metaDir, 0755)
	if err != nil {
		logrus.Infof("Failed to create .meta dir: %v", err)
		return false
	}

	// write build metadata
	extraBlobs := []OpaqueBlob{}
	metadata := getMetadata()
	metadata.Buildno = buildOpts.buildNo
	if hostname, err := os.Hostname(); err == nil {
		metadata.BuildHost = hostname
	}

	// write the normalized config to metadata
	smithJson, err := json.Marshal(pkg)
	if err == nil {
		newBlob := OpaqueBlob{"application/vnd.smith.spec+json", smithJson}
		extraBlobs = append(extraBlobs, newBlob)
	}

	if packages != nil {
		newBlob := OpaqueBlob{"application/vnd.smith.packages",
			[]byte(strings.Join(packages, "\n"))}
		extraBlobs = append(extraBlobs, newBlob)
	}

	// perform overlay
	logrus.Infof("Performing overlay")
	files := []string{rootfs}
	if pkg.Parent != "" {
		files = append(files, strings.Split(pkg.Parent, ":")[0])
	}
	err = CopyTree(path, buildDir, files, nil, pkg.Nss, false, false)
	if err != nil {
		logrus.Infof("Failed to copy %v to %v: %v", path, buildDir, err)
		return false
	}

	// pack
	logrus.Infof("Packing image into %v", outpath)
	if err := WriteOciFromBuild(pkg, buildDir, outpath, metadata, extraBlobs); err != nil {
		logrus.Infof("Failed to pack dir into %v: %v", outpath, err)
		return false
	}
	return true
}

func rootfsDir(buildDir string, rootDir string) (string, error) {
	outputDir, err := filepath.Abs(filepath.Join(buildDir, rootDir))
	if err != nil {
		return "", err
	}
	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		return "", err
	}
	dirs := []string{"dev", "read", "write", "run", "proc", "sys"}
	for _, dir := range dirs {
		err = os.MkdirAll(filepath.Join(outputDir, dir), 0755)
		if err != nil {
			return "", err
		}
	}
	return outputDir, nil
}

func readablePathsFromExecutor(ex executor, paths []string) error {
	// chmod anything we are pulling out to be readable by group + others
	if len(paths) == 0 {
		return nil
	}
	arg := []string{"-R", "go+rX"}
	arg = append(arg, paths...)
	_, stderr, err := ex("chmod", arg...)
	if err != nil {
		logrus.Warnf("chmod failed: %v", strings.TrimSpace(stderr))
		return err
	}
	return nil

}

func buildMockDebuginfo(buildOpts *buildOptions, outputDir string, pkg *ConfigDef, pkgMfst *RPMManifest) error {
	baseDir, err := MockBuildDebuginfo(pkgMfst, &pkg.Mock)
	if err != nil {
		return err
	}

	paths := pkgMfst.FindDebugInfo(baseDir)
	if len(pkg.Mock.DebugPaths) > 0 {
		paths = append(paths, pkg.Mock.DebugPaths...)
	}

	executor := func(name string, arg ...string) (string, string, error) {
		return MockExecuteQuiet(pkg.Mock.Config, name, arg...)
	}

	if err := readablePathsFromExecutor(executor, paths); err != nil {
		logrus.Warnf("Could not make paths readable: %v", err)
	}

	err = CopyTree(baseDir, outputDir, paths, nil, false, false, true)
	if err != nil {
		return err
	}

	pkgMfst.ClearDebugState()
	return nil
}

func buildMock(buildOpts *buildOptions, outputDir string, pkg *ConfigDef, pkgMfst *RPMManifest) error {
	baseDir, err := MockBuild(pkg.Package, buildOpts.fast, &pkg.Mock)
	if err != nil {
		return err
	}

	executor := func(name string, arg ...string) (string, string, error) {
		return MockExecuteQuiet(pkg.Mock.Config, name, arg...)
	}

	if err := SetSoPathsFromExecutor(executor); err != nil {
		return err
	}

	if err := readablePathsFromExecutor(executor, pkg.Paths); err != nil {
		logrus.Warnf("Could not make paths readable: %v", err)
	}
	err = CopyTree(baseDir, outputDir, pkg.Paths, pkg.Excludes, pkg.Nss, true, true)
	if err != nil {
		return err
	}

	err = pkgMfst.UpdateManifest(baseDir, outputDir, pkg.Mock.Config)
	if err != nil {
		return err
	}

	if pkg.Mock.DebugInfo {
		return buildMockDebuginfo(buildOpts, outputDir, pkg, pkgMfst)
	}

	return nil
}

func buildOci(buildOpts *buildOptions, outputDir string, pkg *ConfigDef) error {
	uid, gid := os.Getuid(), os.Getgid()
	unpackDir := filepath.Join(os.TempDir(), "smith-unpack-"+strconv.Itoa(uid))

	executor := func(name string, arg ...string) (string, string, error) {
		attr := &syscall.SysProcAttr{
			Chroot: unpackDir,
		}
		attr, err := setAttrMappings(attr, uid, gid)
		if err != nil {
			return "", "", err
		}
		return execute.AttrExecuteQuiet(attr, name, arg...)
	}

	var image *Image
	var err error
	if strings.HasPrefix(pkg.Package, "http://") ||
		strings.HasPrefix(pkg.Package, "https://") {
		image, err = imageFromRemote(pkg.Package, buildOpts.insecure)
	} else {
		image, err = imageFromFile(pkg.Package)
	}
	if err != nil {
		return err
	}
	// pull the existing data out of the image
	if len(pkg.Cmd) == 0 {
		pkg.Cmd = image.Config.Config.Entrypoint
		pkg.Cmd = append(pkg.Cmd, image.Config.Config.Cmd...)
	}
	if len(pkg.Env) == 0 {
		pkg.Env = image.Config.Config.Env
	}
	if len(pkg.Dir) == 0 {
		pkg.Dir = image.Config.Config.WorkingDir
	}
	if len(pkg.Ports) == 0 {
		pkg.Ports = image.Config.Config.ExposedPorts
	}

	if !buildOpts.fast {
		// remove directory
		logrus.Infof("Removing %v", unpackDir)
		if err := os.RemoveAll(unpackDir); err != nil {
			logrus.Infof("Failed to remove %v: %v", unpackDir, err)
		}
	}

	// only unpack if the directory doesn't already exist
	if _, err := os.Stat(unpackDir); os.IsNotExist(err) {
		if err := ExtractOci(image, unpackDir); err != nil {
			return err
		}
		if err := readablePathsFromExecutor(executor, pkg.Paths); err != nil {
			logrus.Warnf("Could not make paths readable: %v", err)
		}
	}

	if err := SetSoPathsFromExecutor(executor); err != nil {
		return err
	}

	err = CopyTree(unpackDir, outputDir, pkg.Paths, pkg.Excludes, pkg.Nss, true, true)
	if err != nil {
		return err
	}

	return nil
}
