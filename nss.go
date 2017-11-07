package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Sirupsen/logrus"
)

const (
	// DefaultID is the default uid and gid for smith
	DefaultID = 10
)

// PopulateNss populates the passwd, group and nsswitch.conf files if necessary
// in the etc directory relative to the outputDir parameter. The users in the
// users parameter will be populated innto the etc/passwd and the groups in the
// groups parameter will be polulated into the etc/groups file along with some
// predetermined users and groups required in the container image.  New uids
// and gids will start at idStart The etc/nsswitch.conf is set to lookup
// everything based on files and is not influenced by the function's
// parameters.  This function will overwrite any existing passwd, group or
// nsswitch.conf files. It returns true if the user string specified a
// non-numeric user or group.
func PopulateNss(outputDir string, user string, groups []string, nss bool) (bool, error) {
	uid, gid, u, g, n := ParseUser(user)
	if !nss && !n && len(groups) == 0 {
		return false, nil
	}
	etcDir := filepath.Join(outputDir, "etc")
	logrus.Infof("Populating nss with %s(%d):%s(%d)", u, uid, g, gid)
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		return false, err
	}
	gs := []string{g}
	gids := []int{gid}
	names := []string{""}
	groupid := gid
	for _, group := range groups {
		if getId(defaultGroups(), group) != -1 {
			continue
		}
		gs = append(gs, group)
		groupid += 1
		names = append(names, u)
		gids = append(gids, groupid)
	}
	if err := populateGroups(etcDir, gs, gids, names); err != nil {
		return false, err
	}
	if err := populateUsers(etcDir, []string{u}, []int{uid}); err != nil {
		return false, err
	}
	if err := populateNsswitch(etcDir); err != nil {
		return false, err
	}
	return true, nil
}

// ParseUser parses a user string in oci format and returns uid, gid, user,
// group. Uid and gid default to DefaultID if they are not set.
func ParseUser(user string) (int, int, string, string, bool) {
	parts := strings.Split(user, ":")
	u := parts[0]
	g := ""
	if len(parts) > 1 {
		g = parts[1]
	}
	var uid int
	var gid int
	users := defaultUsers()
	groups := defaultGroups()
	uid, gid = getId(users, u), getId(groups, g)
	nss := false
	if val, err := strconv.Atoi(u); err == nil {
		uid = val
		u = getName(users, uid)
	} else if u != "" {
		nss = true
	}
	if val, err := strconv.Atoi(g); err == nil {
		gid = val
		g = getName(groups, gid)
	} else if g != "" {
		nss = true
	}
	if uid == -1 {
		uid = DefaultID
	}
	if gid == -1 {
		gid = DefaultID
	}
	if u == "" {
		u = "smith"
	}
	if g == "" {
		g = "smith"
	}
	return uid, gid, u, g, nss
}

var dUsers []string
var dGroups []string

func defaultUsers() []string {
	if dUsers == nil {
		dUsers = []string{
			"root:x:0:0:root:/write:",
			"daemon:x:1:1:daemon:/write:",
			"bin:x:2:2:bin:/write:",
			"sys:x:3:3:sys:/write:",
		}
	}
	return dUsers
}

func defaultGroups() []string {
	if dGroups == nil {
		dGroups = []string{
			"root:x:0:",
			"daemon:x:1:",
			"bin:x:2:",
			"sys:x:3:",
			"adm:x:4:",
			"tty:x:5:",
		}
	}
	return dGroups
}

func getId(items []string, name string) int {
	for _, s := range items {
		parts := strings.Split(s, ":")
		if name == parts[0] && len(parts) > 2 {
			val, err := strconv.Atoi(parts[2])
			if err == nil {
				return val
			}
		}
	}
	return -1
}

func getName(items []string, id int) string {
	for _, s := range items {
		parts := strings.Split(s, ":")
		if len(parts) > 2 {
			val, err := strconv.Atoi(parts[2])
			if err == nil && id == val {
				return parts[0]
			}
		}
	}
	return ""
}

func populateUsers(etcDir string, users []string, ids []int) error {
	s := defaultUsers()
	min := len(s)
	for i, user := range users {
		id := ids[i]
		if id < min {
			continue
		}
		s = append(s, fmt.Sprintf("%s:x:%d:%d:%s:/write", user, id, id, user))
	}
	path := filepath.Join(etcDir, "passwd")
	if err := ioutil.WriteFile(path, []byte(strings.Join(s, "\n")), 0644); err != nil {
		return err
	}
	return nil
}

func populateGroups(etcDir string, groups []string, ids []int, names []string) error {
	s := defaultGroups()
	min := len(s)
	for i, group := range groups {
		id := ids[i]
		if id < min {
			continue
		}
		members := names[i]
		s = append(s, fmt.Sprintf("%s:x:%d:%s", group, id, members))
	}
	path := filepath.Join(etcDir, "group")
	if err := ioutil.WriteFile(path, []byte(strings.Join(s, "\n")), 0644); err != nil {
		return err
	}
	return nil
}

func populateNsswitch(etcDir string) error {
	s := []string{
		"passwd:     files",
		"shadow:     files",
		"group:      files",
		"hosts:      files dns",
	}
	path := filepath.Join(etcDir, "nsswitch.conf")
	if err := ioutil.WriteFile(path, []byte(strings.Join(s, "\n")), 0644); err != nil {
		return err
	}
	return nil
}
