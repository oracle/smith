package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/oracle/smith/execute"
)

func dependenciesEqual(t *testing.T, a, b map[string]struct{}) {
	if len(a) != len(b) {
		t.Fatalf("Different number of dependencies: %v, %v", len(a), len(b))
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			t.Fatalf("Dependency %v isn't in result", k)
		}
	}
}

func skipIfNotLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test. Does not work on non-linux operating systems.")
	}
}

func TestDeps(t *testing.T) {
	skipIfNotLinux(t)
	target, _ := filepath.Abs("/usr/bin/env")
	stdout, _, _ := execute.ExecuteQuiet("./deps.sh", target)
	expected := map[string]struct{}{}
	for _, dep := range strings.Split(strings.TrimSpace(stdout), "\n") {
		expected[dep] = struct{}{}
	}

	err := SetSoPathsFromExecutor(execute.ExecuteQuiet, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	result, err := Deps("", target, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	dependenciesEqual(t, expected, result)
}

func TestDepsNss(t *testing.T) {
	skipIfNotLinux(t)
	target, _ := filepath.Abs("/usr/bin/env")
	stdout, _, _ := execute.ExecuteQuiet("./deps-nss.sh", target)
	expected := map[string]struct{}{}
	for _, dep := range strings.Split(strings.TrimSpace(stdout), "\n") {
		expected[dep] = struct{}{}
	}

	err := SetSoPathsFromExecutor(execute.ExecuteQuiet, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	result, err := Deps("", target, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	dependenciesEqual(t, expected, result)
}

const fakeLdconfig = `/lib:
					libffi.so.6 -> libffi.so.6.0.1
/lib64:
					libc.so.6 -> libc-2.17.so
`

func TestFindLibrary(t *testing.T) {
	exec := func(name string, arg ...string) (string, string, error) {
		return fakeLdconfig, "", nil
	}
	err := SetSoPathsFromExecutor(exec, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if FindLibrary("libffi.so.6", "", []string{}) != "/lib/libffi.so.6" {
		t.Fatalf("Libraries don't match")
	}
	if FindLibrary("libc.so.6", "", []string{}) != "/lib64/libc.so.6" {
		t.Fatalf("Libraries don't match")
	}
}

func TestFindLibrarySearch(t *testing.T) {
	exec := func(name string, arg ...string) (string, string, error) {
		return "", "", nil
	}
	err := SetSoPathsFromExecutor(exec, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if FindLibrary("deps_fake.so", "", []string{"."}) != "deps_fake.so" {
		t.Fatalf("Libraries don't match")
	}
}
