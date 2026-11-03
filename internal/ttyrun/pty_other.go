//go:build !linux

package ttyrun

import (
	"errors"
	"os"
	"os/exec"
)

func startPTY(*exec.Cmd, int, int) (*os.File, error) {
	return nil, errors.New("interactive steps run in the background on Linux machines only; here: run the step in a terminal")
}
