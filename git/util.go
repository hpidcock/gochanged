package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/juju/errors"
)

func Root(dir string) (string, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return "", errors.Annotate(err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func Read(dir, treeish, file string) ([]byte, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := exec.Command("git", "-C", dir, "show", fmt.Sprintf("%s:%s", treeish, file))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		errStr := stderr.String()
		if strings.Contains(errStr, "does not exist in") {
			return nil, errors.NewNotFound(err, errStr)
		}
		return nil, errors.Annotate(err, errStr)
	}
	return stdout.Bytes(), nil
}
