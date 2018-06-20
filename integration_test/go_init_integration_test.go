// Copyright 2016 Palantir Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/palantir/godel/pkg/products/v2/products"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palantir/go-java-launcher/init/lib"
)

var files = []string{lib.LauncherStaticFile, lib.LauncherCustomFile, lib.OutputFile, lib.Pidfile}

func setup(t *testing.T) {
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0777))
	}

	require.NoError(t, os.Link("testdata/launcher-static.yml", lib.LauncherStaticFile))
	require.NoError(t, os.Link("testdata/launcher-custom.yml", lib.LauncherCustomFile))
}

func teardown(t *testing.T) {
	for _, file := range files {
		require.NoError(t, os.RemoveAll(strings.Split(file, "/")[0]))
	}
}

func writePid(t *testing.T, pid int) {
	require.NoError(t, ioutil.WriteFile(lib.Pidfile, []byte(strconv.Itoa(pid)), 0644))
}

func readPid(t *testing.T) int {
	pidBytes, err := ioutil.ReadFile(lib.Pidfile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(pidBytes))
	require.NoError(t, err)
	return pid
}

func TestInitStart_DoesNotRestartRunning(t *testing.T) {
	setup(t)
	defer teardown(t)

	writePid(t, os.Getpid())
	exitCode, stderr := runInit(t, "start")

	assert.Equal(t, 0, exitCode)
	assert.Equal(t, os.Getpid(), readPid(t))
	assert.Empty(t, stderr)
}

func TestInitStart_StartsNotRunningPidfileExists(t *testing.T) {
	setup(t)
	defer teardown(t)

	writePid(t, 99999)
	exitCode, stderr := runInit(t, "start")

	assert.Equal(t, 0, exitCode)
	time.Sleep(time.Second) // Wait for JVM to start and print output
	startupLogBytes, err := ioutil.ReadFile(lib.OutputFile)
	require.NoError(t, err)
	startupLog := string(startupLogBytes)
	assert.Contains(t, startupLog, "Using JAVA_HOME")
	assert.Contains(t, startupLog, "main method")
	assert.Empty(t, stderr)
}

func TestInitStart_StartsNotRunningPidfileDoesNotExist(t *testing.T) {
	setup(t)
	defer teardown(t)

	exitCode, stderr := runInit(t, "start")

	assert.Equal(t, 0, exitCode)
	time.Sleep(time.Second) // Wait for JVM to start and print output
	startupLogBytes, err := ioutil.ReadFile(lib.OutputFile)
	require.NoError(t, err)
	startupLog := string(startupLogBytes)
	assert.Contains(t, startupLog, "Using JAVA_HOME")
	assert.Contains(t, startupLog, "main method")
	assert.Empty(t, stderr)
}

func TestInitStatus_Running(t *testing.T) {
	setup(t)
	defer teardown(t)

	writePid(t, os.Getpid())
	exitCode, stderr := runInit(t, "status")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
}

func TestInitStatus_NotRunningPidfileExists(t *testing.T) {
	setup(t)
	defer teardown(t)

	writePid(t, 99999)
	exitCode, stderr := runInit(t, "status")

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stderr, "pidfile exists but process is not running")
}

func TestInitStatus_NotRunningPidfileDoesNotExist(t *testing.T) {
	setup(t)
	defer teardown(t)

	exitCode, stderr := runInit(t, "status")

	assert.Equal(t, 3, exitCode)
	assert.Contains(t, stderr, "failed to read pidfile: open var/run/service.pid: no such file or directory")
}

func TestInitStop_StopsRunningAndFailsRunningDoesNotTerminate(t *testing.T) {
	setup(t)
	defer teardown(t)

	// Stoppable process gets stopped.
	require.NoError(t, exec.Command("/bin/sh", "-c", "/bin/sleep 10000 &").Run())
	pidBytes, err := exec.Command("pgrep", "-f", "sleep").Output()
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.Split(string(pidBytes), "\n")[0])
	require.NoError(t, err)
	writePid(t, pid)
	exitCode, stderr := runInit(t, "stop")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
	_, err = ioutil.ReadFile(lib.Pidfile)
	assert.EqualError(t, err, "open var/run/service.pid: no such file or directory")

	// Reset since this is really two tests we have to run sequentially.
	teardown(t)
	setup(t)

	// Unstoppable process does not get stopped.
	require.NoError(t, exec.Command("/bin/sh", "-c", "trap '' 15; /bin/sleep 10000 &").Run())
	pidBytes, err = exec.Command("pgrep", "-f", "sleep").Output()
	require.NoError(t, err)
	pid, err = strconv.Atoi(strings.Split(string(pidBytes), "\n")[0])
	require.NoError(t, err)
	writePid(t, pid)
	exitCode, stderr = runInit(t, "stop")

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stderr, fmt.Sprintf("failed to stop process: failed to wait for process to stop: process with "+
		"pid '%d' did not stop within 240 seconds", pid))

	process, _ := os.FindProcess(readPid(t))
	require.NoError(t, process.Signal(syscall.SIGKILL))
}

func TestInitStop_RemovesPidfileNotRunningPidfileExists(t *testing.T) {
	setup(t)
	defer teardown(t)

	writePid(t, 99999)
	exitCode, stderr := runInit(t, "stop")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
	_, err := ioutil.ReadFile(lib.Pidfile)
	assert.EqualError(t, err, "open var/run/service.pid: no such file or directory")
}

func TestInitStop_DoesNothingNotRunningPidfileDoesNotExist(t *testing.T) {
	exitCode, stderr := runInit(t, "stop")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
}

// Adapted from Stack Overflow: http://stackoverflow.com/questions/10385551/get-exit-code-go
func runInit(t *testing.T, args ...string) (int, string) {
	var errbuf bytes.Buffer
	cli, err := products.Bin("go-init")
	require.NoError(t, err)
	cmd := exec.Command(cli, args...)
	cmd.Stderr = &errbuf
	err = cmd.Run()
	stderr := errbuf.String()

	if err != nil {
		// try to get the exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			return ws.ExitStatus(), stderr
		} else {
			// This will happen (in OSX) if `name` is not available in $PATH,
			// in this situation, exit code could not be get, and stderr will be
			// empty string very likely, so we use the default fail code, and format err
			// to string and set to stderr
			log.Printf("Could not get exit code for failed program: %v, %v", cli, args)
			if stderr == "" {
				stderr = err.Error()
			}
			return -1, stderr
		}
	} else {
		// success, exitCode should be 0 if go is ok
		ws := cmd.ProcessState.Sys().(syscall.WaitStatus)
		return ws.ExitStatus(), stderr
	}
}