package git

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"
	"path"
	"strings"

	"github.com/juju/errors"
)

func DiffNames(dir, treeish string) ([]string, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := exec.Command("git", "-C", dir, "diff", "--name-status", treeish, "--")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return nil, errors.Annotate(err, stderr.String())
	}
	reader := bufio.NewReader(stdout)
	files := []string(nil)
	for {
		str, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.Trace(err)
		}
		str = strings.TrimRight(str, "\n")
		fields := strings.Fields(str)
		if len(fields) < 2 {
			return nil, errors.Errorf("bad line in git-diff: %s", str)
		}
		diffType := fields[0][0]
		diffAttr := fields[0][1:]
		_ = diffAttr
		// Added (A), Copied (C), Deleted (D), Modified (M), Renamed (R)
		switch diffType {
		case 'A', 'D', 'M':
			files = append(files, path.Join(dir, fields[1]))
		case 'R':
			files = append(files, path.Join(dir, fields[1]))
			files = append(files, path.Join(dir, fields[2]))
		//case 'C':
		default:
			return nil, errors.Errorf("bad line in git-diff: %s", str)
		}
	}
	return files, nil
}
