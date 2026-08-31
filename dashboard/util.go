package dashboard

import (
	"bytes"
	"os/exec"
)

// runCmdOut runs a command and returns combined stdout+stderr output.
func runCmdOut(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runCmdDir runs a command in a specific directory and returns combined
// stdout+stderr output.
func runCmdDir(dir, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
