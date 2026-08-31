package main

import (
	"bytes"
	"os/exec"
)

// CommandRunner abstracts system command execution for testability.
// Production code uses OSRunner; tests can substitute a MockRunner to
// record calls and return canned output without spawning real processes.
type CommandRunner interface {
	// Run executes name with args and returns combined stdout+stderr output.
	Run(name string, args ...string) (string, error)
	// RunDir executes name with args in the given working directory and
	// returns combined stdout+stderr output.
	RunDir(dir, name string, args ...string) (string, error)
}

// OSRunner is the production CommandRunner implementation backed by os/exec.
type OSRunner struct{}

// Run executes name with args and returns combined stdout+stderr.
func (r *OSRunner) Run(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	return buf.String(), cmd.Run()
}

// RunDir executes name with args in dir and returns combined stdout+stderr.
func (r *OSRunner) RunDir(dir, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	return buf.String(), cmd.Run()
}

// defaultRunner is the global production runner used by runCmd and runCmdDir.
// Tests may replace this with a MockRunner to intercept system calls.
var defaultRunner CommandRunner = &OSRunner{}
