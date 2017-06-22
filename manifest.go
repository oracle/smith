package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sirupsen/logrus"
)

// NT_GNU_BUILD_ID is defined in <elf.h>
const (
	NT_GNU_BUILD_ID = 3
	DEBUG_INFO_DIR  = "/usr/lib/debug"
	BUILD_ID_DIR    = ".build-id"
	DWZ_DIR         = ".dwz"
)

// DebugFile maps an ELF file to its corresponding debug file(s).  If one or
// more exist.
type DebugFile struct {
	Filename  string
	Buildid   string
	Debuglink string
	Debugpkg  string
}

type RPMManifest struct {
	PkgsInstalled      map[string]bool
	PkgsInstalledDebug map[string]bool
	PkgsWantedDebug    map[string]bool
	Files              []string
	ElfFiles           map[string]*DebugFile
}

func NewRPMManifest() *RPMManifest {
	return &RPMManifest{map[string]bool{}, map[string]bool{}, map[string]bool{}, []string{}, map[string]*DebugFile{}}
}

// ClearDebugState erases the debug contents of a manifest so that packages and
// files used for one packaging transaction aren't copied a 2nd time in
// another.  It does not touch PkgsInstalled, since this is cumulative and
// examined at the end of all pkg transactions.
func (rm *RPMManifest) ClearDebugState() {
	rm.PkgsInstalledDebug = map[string]bool{}
	rm.PkgsWantedDebug = map[string]bool{}
	rm.Files = []string{}
	rm.ElfFiles = map[string]*DebugFile{}
}

func (rm *RPMManifest) DebugCandidates(debugDeps []string) []string {
	ddm := make(map[string]string)
	s := []string{}

	// In some cases we may auto-compute a debug dependency that we can't find
	// on a remote server.  If the user has given us that dependency as a RPM
	// file, elide the computed dependency in favor of the user supplied one.
	// This prevents Mock from issuing misleading error messages about missing
	// packages.
	for _, ds := range debugDeps {
		ddm[strings.TrimSuffix(ds, ".rpm")] = ds
	}

	for key := range rm.PkgsWantedDebug {
		depstr, ok := ddm[key]
		if ok {
			s = append(s, depstr)
			delete(ddm, key)
		} else {
			s = append(s, key)
		}
	}

	for _, remainingDep := range ddm {
		s = append(s, remainingDep)
	}
	sort.Strings(s)
	return s
}

func (rm *RPMManifest) EmitPackages(outpath string) error {
	s := []string{}
	for key := range rm.PkgsInstalled {
		s = append(s, key)
	}
	sort.Strings(s)

	if err := ioutil.WriteFile(outpath, []byte(strings.Join(s, "\n")), 0644); err != nil {
		return err
	}

	return nil
}

func (rm *RPMManifest) FindDebugInfo(baseDir string) []string {
	dbgMap := make(map[string]bool)
	outstr := []string{}

	for _, dbf := range rm.ElfFiles {
		// Don't call findDebugInfo on DebugFiles that never successfully
		// had their debuginfo package installed.
		if _, ok := rm.PkgsInstalledDebug[dbf.Debugpkg]; !ok {
			logrus.Debugf("Skipping FindDebugInfo for %v because %v was not installed", dbf.Filename, dbf.Debugpkg)
			continue
		}
		sa := findDebugInfo(baseDir, dbf)
		for _, s := range sa {
			dbgMap[s] = true
		}
	}
	for key := range dbgMap {
		outstr = append(outstr, key)
	}
	// As a final step, read the contents of the usr/lib/debug and
	// usr/lib/debug/usr directory.  Ensure that any symlinks in that directory
	// are copied, since .build-id paths may depend upon its presence.
	sl := copyDebugSymlinks(baseDir, DEBUG_INFO_DIR)
	outstr = append(outstr, sl...)
	sl = copyDebugSymlinks(baseDir, filepath.Join(DEBUG_INFO_DIR, "usr"))
	outstr = append(outstr, sl...)

	return outstr
}

// FindDebugInstalled will update the RPMManifest to include a list of debug
// packages that were installed into the mock root.  This is used so that a) we
// can tell the user if any packages are missing, and b) we don't attempt to
// look for debug files that we're never going to find.
func (rm *RPMManifest) FindDebugInstalled(rootpath string, wanted []string, config string) error {
	stdout, _, err := MockExecuteQuiet(config, "rpm -qa")
	if err != nil {
		return err
	}
	for _, pkg := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.Contains(pkg, "debuginfo") {
			logrus.Debugf("Installed debuginfo package %v", pkg)
		}
		rm.PkgsInstalledDebug[pkg] = true
	}

	for _, s := range wanted {
		s = strings.TrimSuffix(s, ".rpm")
		if _, ok := rm.PkgsInstalledDebug[s]; !ok {
			logrus.Warnf("No debug package %v is available", s)
		}
	}

	return nil
}

func (rm *RPMManifest) updateData(rootpath, config string) error {
	if len(rm.Files) != 0 {
		tmpfile, err := ioutil.TempFile("", "manifest-")
		if err != nil {
			return err
		}
		defer os.Remove(tmpfile.Name())

		data := []byte(strings.Join(rm.Files, "\n"))
		if _, err := tmpfile.Write(data); err != nil {
			return err
		}
		if err := tmpfile.Close(); err != nil {
			return err
		}

		if err := MockCopy(config, tmpfile.Name(), "manifest-allfiles"); err != nil {
			return err
		}

		cmd := "cat manifest-allfiles | xargs rpm -qf "
		stdout, _, err := MockExecuteQuiet(config, cmd)
		if err != nil {
			return err
		}

		for _, pkg := range strings.Split(strings.TrimSpace(stdout), "\n") {
			rm.PkgsInstalled[pkg] = true
		}
	}

	if len(rm.ElfFiles) != 0 {
		tmpfile, err := ioutil.TempFile("", "manifest-")
		if err != nil {
			return err
		}
		defer os.Remove(tmpfile.Name())

		keys := make([]string, 0, len(rm.ElfFiles))
		for k := range rm.ElfFiles {
			keys = append(keys, k)
		}

		data := []byte(strings.Join(keys, "\n"))
		if _, err := tmpfile.Write(data); err != nil {
			return err
		}
		if err := tmpfile.Close(); err != nil {
			return err
		}

		if err := MockCopy(config, tmpfile.Name(), "manifest-debugfiles"); err != nil {
			return err
		}

		cmd := "for a in `cat manifest-debugfiles`; " +
			"do echo -n \"$a \"; " +
			"rpm -q -f --qf \"%{ARCH} %{SOURCERPM}\\n\" $a; " +
			"done"
		stdout, _, err := MockExecuteQuiet(config, cmd)
		if err != nil {
			return err
		}

		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			parts := strings.Split(line, " ")
			if len(parts) != 3 {
				return fmt.Errorf("rpm -qf data incorrect")
			}
			file := parts[0]
			arch := parts[1]
			srpm := parts[2]
			if arch != "noarch" && srpm != "(none)" {
				dcn := srpmToDebug(srpm, arch)
				rm.PkgsWantedDebug[dcn] = true
				rm.ElfFiles[file].Debugpkg = dcn
			}
		}
	}
	return nil
}

// UpdateManifest walks through the output directory looking for files.  It
// will identify the packages in the mock root that own these files, and check
// the files to see if they need debug information.  Once it has done this, it
// will update the manifest accordingly.
func (rm *RPMManifest) UpdateManifest(rootpath, outpath, config string) error {
	var err error

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := os.Chdir(outpath); err != nil {
		return err
	}

	err = filepath.Walk(".", storeFiles(rootpath, rm))

	// reset path regardless of error
	os.Chdir(dir)

	if err != nil {
		return err
	}

	if err := rm.updateData(rootpath, config); err != nil {
		return err
	}
	return nil
}

func align4b(ofst uint32) uint32 {
	if ofst%4 != 0 {
		ofst = (ofst + 4) - (ofst % 4)
	}

	return ofst
}

// buildIDToPath breaks a build id into hash components that are used to
// identify the files on disk.  This is presently the first two characters of
// the hash as the directory, and the remainder as the file's name.
func buildIDToPath(buildID string) string {
	return buildID[:2] + string(filepath.Separator) + buildID[2:]
}

func checkFileDebuglink(filename string) (*DebugFile, error) {
	var err error
	var fp *elf.File
	var buildID string
	var debugLink string

	if fp, err = elf.Open(filename); err != nil {
		return nil, err
	}
	defer fp.Close()

	ft := fp.FileHeader.Type
	if ft != elf.ET_EXEC && ft != elf.ET_DYN && ft != elf.ET_REL {
		return nil, fmt.Errorf("ELF file '%s' is not type EXEC, DYN, or REL", filename)
	}

	buildIDSect := fp.Section(".note.gnu.build-id")
	dbgLinkSect := fp.Section(".gnu_debuglink")

	// Most build artifacts should have a build-id.  However a debuglink
	// should only be inserted when the debugger needs to look for separate
	// symbol/debug files.  Take the presence of both sections to mean that
	// this is a binary with separate debug info.
	if buildIDSect == nil || dbgLinkSect == nil {
		return nil, fmt.Errorf("ELF file '%s' does not have separate debug info", filename)
	}

	if buildID, err = getBuildid(buildIDSect); err != nil {
		return nil, err
	}

	if debugLink, err = getDebugLink(dbgLinkSect); err != nil {
		return nil, err
	}
	return &DebugFile{filename, buildID, debugLink, ""}, nil
}

func copyDebugSymlinks(baseDir string, targetdir string) []string {
	symlinks := []string{}

	dirEnts, err := ioutil.ReadDir(filepath.Join(baseDir, targetdir))
	if err != nil {
		return symlinks
	}

	dir := filepath.Join(baseDir, targetdir)
	for _, de := range dirEnts {
		if de.Mode()&os.ModeSymlink != 0 {
			fn, err := filepath.Rel(baseDir, filepath.Join(dir, de.Name()))
			if err == nil {
				symlinks = append(symlinks, fn)
			}
		}
	}
	return symlinks
}

// findDebugInfo locates the associated file paths that need to be copied into
// the image.  This is probably: 1) buildID symlinks to both the binary and the
// debug file; 2) the .debug file itself; and 3) a dwz file for the package, if
// the debug file contains a .gnu_altdebuglink section.
func findDebugInfo(baseDir string, dfp *DebugFile) []string {
	var err error
	var filePaths []string

	filePaths = []string{}

	// First, construct the path to the .debug file and the buildID symlinks
	dbgPath := filepath.Join(baseDir, DEBUG_INFO_DIR, dfp.Filename+".debug")
	if _, err = os.Stat(dbgPath); err != nil {
		logrus.Warnf("Unable to locate .debug file for %v", dfp.Filename)
		return []string{}
	}
	// Re-evaluate the path against any symlinks it contains.  We want to pull
	// in the actual debug file, instead of dereferencing the link.
	if dbgPath, err = filepath.EvalSymlinks(dbgPath); err != nil {
		return []string{}
	}
	if dbgPath, err = filepath.Rel(baseDir, dbgPath); err != nil {
		return []string{}
	}
	logrus.Debugf("Found .debug file %v", dbgPath)
	filePaths = append(filePaths, dbgPath)

	buildIDPath := filepath.Join(baseDir, DEBUG_INFO_DIR, BUILD_ID_DIR, buildIDToPath(dfp.Buildid))
	if _, err = os.Stat(buildIDPath); err != nil {
		logrus.Warnf("Unable to locate build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
		return []string{}
	}

	buildIDDebugPath := buildIDPath + ".debug"
	if buildIDPath, err = filepath.Rel(baseDir, buildIDPath); err != nil {
		logrus.Errorf("Unable to Rel() build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
		return []string{}
	}
	logrus.Debugf("Found build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
	filePaths = append(filePaths, buildIDPath)

	if _, err = os.Stat(buildIDDebugPath); err != nil {
		logrus.Warnf("Unable to locate .debug build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
		return []string{}
	}
	if buildIDDebugPath, err = filepath.Rel(baseDir, buildIDDebugPath); err != nil {
		logrus.Errorf("Unable to Rel() .debug build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
		return []string{}
	}
	logrus.Debugf("Found build-id symlink for %v (%v)", dfp.Filename, dfp.Buildid)
	filePaths = append(filePaths, buildIDDebugPath)

	// Now, crack open the actual .debug file and check for a couple of things.
	// First, that its buildID matches the binary that sent us here.  Second,
	// look to see if we need to dig out a dwz file in addition to the
	// debuginfo itself.  If we wanted to be extra anal, we could check to
	// make sure that there are actually debug sections in the file, but I
	// don't see any point to that yet.
	dbgBuildID, altDbgLink, err := validateDebugFile(filepath.Join(baseDir, dbgPath))
	if err != nil {
		logrus.Errorf("failed to validate .debug file for %v", dfp.Filename)
		return []string{}
	}
	if dbgBuildID != dfp.Buildid {
		logrus.Errorf("build ids on %v don't match: %v vs %v", dfp.Filename, dfp.Buildid, dbgBuildID)
		return []string{}
	}
	if altDbgLink != "" {
		dbgLinkPath := filepath.Join(baseDir, DEBUG_INFO_DIR, DWZ_DIR, filepath.Base(altDbgLink))
		if _, err = os.Stat(dbgLinkPath); err != nil {
			logrus.Errorf(".debug file for %v contains altdebuglink, but the path %v cannot be found", dfp.Filename, dbgLinkPath)
			return []string{}
		}
		dbgLinkPath, err = filepath.Rel(baseDir, dbgLinkPath)
		if err != nil {
			logrus.Errorf("Unable to Rel() altdebuglink for %v", dfp.Filename)
			return []string{}
		}
		logrus.Debugf("Found altdebuglink for %v: %v", dfp.Filename, dbgLinkPath)
		filePaths = append(filePaths, dbgLinkPath)
	}
	return filePaths
}

func getBuildid(sect *elf.Section) (string, error) {
	if sect == nil {
		return "", fmt.Errorf("Caller passed nil sect*")
	}

	sectName := sect.SectionHeader.Name

	if sect.SectionHeader.Type != elf.SHT_NOTE {
		return "", fmt.Errorf("Section '%s' isn't of type SHT_NOTE", sectName)
	}

	hdrlen := uint32(12)
	sd, err := sect.Data()
	if err != nil {
		return "", err
	}
	// If the section contains a header or less worth of data, it's
	// not interesting.
	if uint32(len(sd)) <= hdrlen {
		return "", fmt.Errorf("Section '%s' doesn't have any data", sectName)
	}
	//
	// ELF notes start with a 3-word header.  Word 1 - name size,
	// Word 2 - description size, Word 3 - Note type.
	// The name and description follow the header, respectively.
	//
	namesz := binary.LittleEndian.Uint32(sd[:4])
	descsz := binary.LittleEndian.Uint32(sd[4:8])
	ntype := binary.LittleEndian.Uint32(sd[8:12])
	ofst := align4b(hdrlen)
	namestr := string(sd[ofst : ofst+(namesz-1)])
	if namestr != "GNU" || ntype != NT_GNU_BUILD_ID {
		return "", fmt.Errorf("Malformed build id note: name '%s' type '%d'", namestr, ntype)
	}

	ofst = align4b(ofst + namesz)
	buildID := fmt.Sprintf("%x", sd[ofst:ofst+descsz])

	return buildID, nil
}

func getDebugLink(sect *elf.Section) (string, error) {
	if sect == nil {
		return "", fmt.Errorf("Caller passed nil sect*")
	}

	sd, err := sect.Data()
	if err != nil {
		return "", err
	}
	// The format of the debuglink entry is a null-terminated string followed
	// by the CRC.
	idx := bytes.IndexByte(sd, 0)
	if idx <= 0 {
		return "", fmt.Errorf("Malformed debug link")
	}
	// We don't care about the CRC, and don't extract it here.

	return string(sd[:idx]), nil
}

func storeFiles(rootdir string, rm *RPMManifest) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if rm == nil {
			return fmt.Errorf("Caller passed a nil rm pointer")
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		abs := filepath.Join("/", path)
		rm.Files = append(rm.Files, abs)

		dbp, err := checkFileDebuglink(path)
		if err != nil {
			return nil
		}
		rm.ElfFiles[abs] = dbp
		return nil
	}
}

func srpmToDebug(srpmName string, pkgArch string) string {
	var rel, ver, epoch, name string

	s := srpmName
	idx := strings.LastIndex(s, ".rpm")
	if idx != -1 {
		s = s[:idx]
	}
	idx = strings.LastIndex(s, ".")
	if idx != -1 {
		s = s[:idx]
	}
	idx = strings.LastIndex(s, "-")
	if idx != -1 {
		rel = s[idx+1:]
		s = s[:idx]
	}
	idx = strings.LastIndex(s, "-")
	if idx != -1 {
		ver = s[idx+1:]
		s = s[:idx]
	}
	idx = strings.Index(s, ":")
	if idx != -1 {
		epoch = s[:idx]
		s = s[idx+1:]
	}
	name = s
	if epoch != "" {
		s = fmt.Sprintf("%s:%s-debuginfo-%s-%s.%s", epoch, name, ver, rel, pkgArch)
	} else {
		s = fmt.Sprintf("%s-debuginfo-%s-%s.%s", name, ver, rel, pkgArch)
	}
	return s
}

func validateDebugFile(filename string) (string, string, error) {
	var err error
	var fp *elf.File
	var buildID string
	var debugAltLink string

	if fp, err = elf.Open(filename); err != nil {
		return "", "", err
	}
	defer fp.Close()

	ft := fp.FileHeader.Type
	if ft != elf.ET_EXEC && ft != elf.ET_DYN && ft != elf.ET_REL {
		return "", "", fmt.Errorf("ELF file '%s' is not type EXEC, DYN, or REL", filename)
	}

	buildIDSect := fp.Section(".note.gnu.build-id")
	dbgAltLinkSect := fp.Section(".gnu_debugaltlink")

	if buildID, err = getBuildid(buildIDSect); err != nil {
		return "", "", err
	}

	// Mercifully, the .gnu_debugaltlink section has the same format as the
	// .gnu_debuglink section.
	debugAltLink, err = getDebugLink(dbgAltLinkSect)

	return buildID, debugAltLink, nil
}
