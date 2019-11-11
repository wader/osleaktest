package osleaktest

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"
	"time"
)

type testReporter struct {
	failed bool
	msg    string
}

func (tr *testReporter) Errorf(format string, args ...interface{}) {
	tr.failed = true
	tr.msg = fmt.Sprintf(format, args...)
}

func (tr *testReporter) Fatal(...interface{}) {}

func tesFn(t *testing.T, shouldFail bool, fn func()) {
	checker := &testReporter{}
	checkFn := Check(checker)

	fn()

	checkFn()

	if checker.failed != shouldFail {
		if shouldFail {
			t.Errorf("failed to detect leak")
		} else {
			t.Errorf("failed when there was no leak (%s)", checker.msg)
		}
	}
}

func testLeak(t *testing.T, fn func()) {
	tesFn(t, true, fn)
}

func testNoLeak(t *testing.T, fn func()) {
	tesFn(t, false, fn)
}

func TestLeakFd(t *testing.T) {
	var r, w *os.File
	testLeak(t, func() {
		r, w, _ = os.Pipe()
	})
	r.Close()
	w.Close()
}

func TestLeakFdBeforeClosed(t *testing.T) {
	r, w, _ := os.Pipe()
	var r2, w2 *os.File
	testLeak(t, func() {
		r2, w2, _ = os.Pipe()
		r.Close()
		w.Close()
	})
	r2.Close()
	w2.Close()
}

func TestLeakFdBeforeClosedNoLeak(t *testing.T) {
	r, w, _ := os.Pipe()
	testNoLeak(t, func() {
		r.Close()
		w.Close()
	})
}

func TestLeakChildProcessBeforeClosed(t *testing.T) {
	cmd1 := exec.Command("cat")
	cmd1.Start()
	testLeak(t, func() {
		cmd1.Process.Kill()
		cmd1.Wait()
		cmd2 := exec.Command("cat")
		cmd2.Start()
	})
}

func TestLeakChildProcessBeforeNoLeak(t *testing.T) {
	cmd1 := exec.Command("cat")
	cmd1.Start()
	testNoLeak(t, func() {
		cmd1.Process.Kill()
		cmd1.Wait()
	})
}

func TestLeakChildProcess(t *testing.T) {
	testLeak(t, func() {
		cmd := exec.Command("cat")
		cmd.Start()
	})
}

func TestLeakTempFile(t *testing.T) {
	testLeak(t, func() {
		ioutil.TempFile("", "testleak")
	})
}

func TestEmptyLeak(t *testing.T) {
	defer Check(t)()
	time.Sleep(time.Second)
}
