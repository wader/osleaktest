// +build linux

// Package osleaktest checks for leaked fds, child processes and temp files.
//
// File descriptors check will not detect if a fd number existing before the
// check start and then is reused by leaky code.
//
// Temp files check requires that $TMPDIR is used (os.TempDir or
// ioutil.Temp* with empty dir argument).
//
// It is important that the returned check function is always called as it
// also restores TMPDIR. Use defer or func() { defer ... }().
//
package osleaktest

// TODO: make it work on osx and windows?
// TODO: possible to use procfs fd inode, ctime etc?
// TODO: more info about leaked fd? socket/pipe etc?

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Timeout how long to wait for resource cleanups
var Timeout = time.Second * 5

func stringSetMinus(as []string, bs []string) []string {
	nmap := map[string]struct{}{}
	for _, a := range as {
		nmap[a] = struct{}{}
	}

	for _, b := range bs {
		delete(nmap, b)
	}

	ns := []string{}
	for n := range nmap {
		ns = append(ns, n)
	}

	return ns
}

func intSetMinus(as []int, bs []int) []int {
	nmap := map[int]struct{}{}
	for _, a := range as {
		nmap[a] = struct{}{}
	}

	for _, b := range bs {
		delete(nmap, b)
	}

	ns := []int{}
	for n := range nmap {
		ns = append(ns, n)
	}

	return ns
}

// TODO: could be done by probing fds with some syscall?
func fdsForCurrentProcess() ([]int, error) {
	fdFiles, err := ioutil.ReadDir("/dev/fd")
	if err != nil {
		return nil, err
	}
	fds := []int{}
	for _, fdFile := range fdFiles {
		fd, _ := strconv.Atoi(fdFile.Name())
		fds = append(fds, fd)
	}

	return fds, nil
}

type stat struct {
	name string
	ppid int
}

func readProcStatForPid(pid int) (stat, error) {
	statBuf, err := ioutil.ReadFile(fmt.Sprintf("/proc/%s/stat", strconv.Itoa(pid)))
	if err != nil {
		return stat{}, err
	}

	statParts := strings.Split(string(statBuf), " ")
	if len(statParts) < 4 {
		return stat{}, fmt.Errorf("failed to split stat for %d", pid)
	}

	ppid, _ := strconv.Atoi(statParts[3])
	return stat{
		name: statParts[1],
		ppid: ppid,
	}, nil
}

// childsForPid number of child processes for pid
// can't be done atomically so only use in controlled environments
func childsForPid(pid int) ([]int, error) {
	procFiles, err := ioutil.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	// build pid => parent pid map
	pidPpid := map[int]int{}
	for _, procFile := range procFiles {
		procPid, err := strconv.Atoi(procFile.Name())
		if err != nil {
			continue
		}

		stat, err := readProcStatForPid(procPid)
		if err != nil {
			continue
		}

		pidPpid[procPid] = stat.ppid
	}

	childs := []int{}
	var collectHelper func(pid int)
	collectHelper = func(pid int) {
		for cpid, ppid := range pidPpid {
			if ppid == pid {
				childs = append(childs, cpid)
				collectHelper(cpid)
			}
		}
	}

	collectHelper(pid)

	return childs, nil
}

// ErrorReporter is a tiny subset of a testing.TB to make testing not such a
// massive pain
type ErrorReporter interface {
	Errorf(format string, args ...interface{})
	Fatal(...interface{})
}

func checkLeak(
	t ErrorReporter,
	timeout time.Duration,
	resource string,
	check func() (leaked interface{}, ok bool)) error {
	deadline := time.Now().Add(timeout)
	for {
		leaked, ok := check()
		if ok {
			break
		}

		if time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		return fmt.Errorf("leaked %s: %v", resource, leaked)
	}

	return nil
}

// Check for leaked fds, child processes, goroutines and temp files.
// Will assume default http transport is used.
// Use defer leaktest.Check(t)() first in test function.
func Check(t ErrorReporter) func() {
	httpTransport, _ := http.DefaultTransport.(*http.Transport)
	return CheckWithTransport(t, httpTransport)
}

// CheckWithTransport same as Check but with own http transport.
func CheckWithTransport(t ErrorReporter, transport *http.Transport) func() {
	fdsBefore, fdsBeforeErr := fdsForCurrentProcess()
	if fdsBeforeErr != nil {
		t.Fatal(fdsBeforeErr)
	}
	childsBefore, childsBeforeErr := childsForPid(os.Getpid())
	if childsBeforeErr != nil {
		t.Fatal(childsBeforeErr)
	}

	testTempDir, testTempDirErr := ioutil.TempDir("", "leaktest")
	if testTempDirErr != nil {
		t.Fatal(testTempDirErr)
	}
	origTMPDIR := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", testTempDir)

	return func() {
		defer os.Setenv("TMPDIR", origTMPDIR)
		defer os.RemoveAll(testTempDir)

		checks := []struct {
			name string
			fn   func() (interface{}, bool)
		}{
			{

				name: "file descriptors",
				fn: func() (interface{}, bool) {
					if transport != nil {
						transport.CloseIdleConnections()
					}

					fdsAfter, fdsAfterErr := fdsForCurrentProcess()
					if fdsAfterErr != nil {
						t.Fatal(fdsAfterErr)
					}

					leaked := intSetMinus(fdsAfter, fdsBefore)
					return leaked, len(leaked) == 0
				},
			},
			{
				name: "child processes",
				fn: func() (interface{}, bool) {
					childsAfter, childsAfterErr := childsForPid(os.Getpid())
					if childsAfterErr != nil {
						t.Fatal(childsAfterErr)
					}

					leaked := intSetMinus(childsAfter, childsBefore)
					fancyPids := []string{}
					for _, pid := range leaked {
						stat, err := readProcStatForPid(pid)
						if err == nil {
							fancyPids = append(fancyPids, fmt.Sprintf("%d %s", pid, stat.name))
						} else {
							fancyPids = append(fancyPids, strconv.Itoa(pid))
						}
					}

					return fancyPids, len(fancyPids) == 0
				},
			},
			{
				name: "temp files",
				fn: func() (interface{}, bool) {
					leaked := []string{}
					filepath.Walk(testTempDir, func(path string, info os.FileInfo, err error) error {
						if path == testTempDir {
							return nil
						}
						leaked = append(leaked, path)
						return nil
					})

					return leaked, len(leaked) == 0
				},
			},
		}
		checkWaitCh := make(chan error, len(checks))

		for _, check := range checks {
			check := check
			go func() {
				checkWaitCh <- checkLeak(t, Timeout, check.name, check.fn)
			}()
		}
		for range checks {
			if err := <-checkWaitCh; err != nil {
				t.Errorf("%s", err)
			}
		}
	}
}
