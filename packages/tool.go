package packages

import (
	"bytes"
	"encoding/json"
	"go/build"
	"os/exec"

	"github.com/juju/errors"
)

func Workspace(buildCtx build.Context, dir string) (string, error) {
	return GoEnv(buildCtx, dir, "GOWORK")
}

func GoEnv(buildCtx build.Context, dir string, env string) (string, error) {
	stdout := &bytes.Buffer{}
	cmd := exec.Command("go", "env", env)
	cmd.Stdout = stdout
	cmd.Dir = dir
	err := cmd.Run()
	if err != nil {
		return "", errors.Trace(err)
	}
	line, err := stdout.ReadString('\n')
	if err != nil {
		return "", errors.Trace(err)
	}
	return line[:len(line)-1], nil
}

func ImportAll(buildCtx build.Context, dir string, packages []string) ([]Package, []Package, error) {
	pkgs, err := internalImportAll(buildCtx, dir, packages, true)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}

	paths := map[string]struct{}{}
	for _, pkg := range pkgs {
		paths[pkg.ImportPath] = struct{}{}
	}

	missing := map[string]struct{}{}
	for _, pkg := range pkgs {
		for _, path := range pkg.Imports {
			if _, ok := paths[path]; !ok {
				missing[path] = struct{}{}
			}
		}
		for _, path := range pkg.TestImports {
			if _, ok := paths[path]; !ok {
				missing[path] = struct{}{}
			}
		}
		for _, path := range pkg.XTestImports {
			if _, ok := paths[path]; !ok {
				missing[path] = struct{}{}
			}
		}
	}

	missingPackages := []string(nil)
	for pkg := range missing {
		missingPackages = append(missingPackages, pkg)
	}

	newPkgs, err := internalImportAll(buildCtx, dir, missingPackages, false)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}

	extraPkgs := []Package(nil)
	for _, pkg := range newPkgs {
		if _, ok := paths[pkg.ImportPath]; !ok {
			paths[pkg.ImportPath] = struct{}{}
			extraPkgs = append(extraPkgs, pkg)
		}
	}

	return pkgs, extraPkgs, nil
}

func internalImportAll(buildCtx build.Context, dir string, packages []string, test bool) ([]Package, error) {
	if len(packages) == 0 {
		return nil, nil
	}

	args := []string{"list", "-e", "-json", "-compiler", buildCtx.Compiler}
	if !test {
		args = append(args, "-deps")
	}

	stdout := &bytes.Buffer{}
	cmd := exec.Command("go", append(append(args, "--"), packages...)...)
	cmd.Stdout = stdout
	cmd.Dir = dir
	err := cmd.Run()
	if err != nil {
		return nil, errors.Trace(err)
	}

	pkgs := []Package(nil)
	decoder := json.NewDecoder(stdout)
	for decoder.More() {
		pkg := Package{}
		err = decoder.Decode(&pkg)
		if err != nil {
			return nil, errors.Trace(err)
		}
		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
