package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/juju/collections/set"
	"github.com/juju/errors"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/hpidcock/gochanged/git"
	"github.com/hpidcock/gochanged/packages"
)

func main() {
	treeish := ""
	why := false
	flag.StringVar(&treeish, "branch", "", "git branch or treeish to diff against")
	flag.BoolVar(&why, "why", false, "explain why each package changed")
	flag.Parse()
	packagesFilter := flag.Args()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	gitRoot, err := git.Root(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	changedFiles, err := git.DiffNames(gitRoot, treeish)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	changedDirectories := make(map[string]bool)
	for _, file := range changedFiles {
		dir := path.Dir(file)
		// TODO: detect if test data has changed.
		changedDirectories[dir] = true
	}

	changedPackages := make(map[string]bool)
	whyChanged := make(map[string][]string)
	addReason := func(importPath string, reasons []string) {
		if !why {
			return
		}
		existingReasons := whyChanged[importPath]
		s := set.NewStrings(existingReasons...)
		for _, reason := range reasons {
			s.Add(reason)
		}
		whyChanged[importPath] = s.Values()
	}
	pkgs, extraPkgs, err := packages.ImportAll(build.Default, wd, packagesFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	allPkgs := append(append([]packages.Package(nil), pkgs...), extraPkgs...)
	for _, v := range pkgs {
		if changedDirectories[path.Clean(v.Dir)] {
			changedPackages[v.ImportPath] = true
			whyChanged[v.ImportPath] = append(whyChanged[v.ImportPath], fmt.Sprintf("package changed %s", v.ImportPath))
		}
	}

	commonModFile := ""
	for _, pkg := range pkgs {
		if commonModFile == "" {
			commonModFile = pkg.Module.GoMod
		} else if commonModFile != pkg.Module.GoMod {
			fmt.Fprintf(os.Stderr, "no common module found for %v", packagesFilter)
			os.Exit(1)
		}
	}

	if !strings.HasPrefix(commonModFile, gitRoot) {
		fmt.Fprintf(os.Stderr, "go.mod is not under git root")
		os.Exit(1)
	}
	goModGitSubpath := strings.TrimLeft(strings.TrimPrefix(commonModFile, gitRoot), string(os.PathSeparator))

	currentModFile, err := ioutil.ReadFile(commonModFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	currentMod, err := modfile.Parse(goModGitSubpath, currentModFile, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	pastModFile, err := git.Read(gitRoot, treeish, goModGitSubpath)
	if errors.Is(err, errors.NotFound) {
		for _, pkg := range packagesFilter {
			fmt.Println(pkg)
		}
		os.Exit(0)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	pastMod, err := modfile.Parse(goModGitSubpath, pastModFile, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if workspace, err := packages.Workspace(build.Default, wd); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	} else if workspace != "" {
		for _, pkg := range packagesFilter {
			if why {
				fmt.Printf("%s => workspace mode", pkg)
			} else {
				fmt.Println(pkg)
			}
		}
		os.Exit(0)
	}

	if currentMod.Go.Version != pastMod.Go.Version {
		for _, pkg := range packagesFilter {
			if why {
				fmt.Printf("%s => go mod version changed", pkg)
			} else {
				fmt.Println(pkg)
			}
		}
		os.Exit(0)
	}

	pastRequires := map[string]module.Version{}
	for _, dep := range pastMod.Require {
		pastRequires[dep.Mod.Path] = dep.Mod
	}
	pastReplace := map[string]*modfile.Replace{}
	for _, rep := range pastMod.Replace {
		pastReplace[rep.Old.Path] = rep
	}

	// Mark different dependencies as changed.
	for _, dep := range currentMod.Require {
		importPath := dep.Mod.Path
		pastVer, ok := pastRequires[importPath]
		if !ok {
			changedPackages[importPath] = true
			whyChanged[importPath] = append(whyChanged[importPath], fmt.Sprintf("new dep %s", importPath))
			continue
		}
		if dep.Mod.Version != pastVer.Version {
			changedPackages[importPath] = true
			whyChanged[importPath] = append(whyChanged[importPath], fmt.Sprintf("changed dep %s", importPath))
			continue
		}
	}
	// Mark different replaces as changed.
	for _, rep := range currentMod.Replace {
		importPath := rep.Old.Path
		pastRep, ok := pastReplace[importPath]
		if !ok {
			changedPackages[importPath] = true
			whyChanged[importPath] = append(whyChanged[importPath], fmt.Sprintf("new replace %s", importPath))
			continue
		}
		delete(pastReplace, importPath)
		if rep.Old.Version != pastRep.Old.Version ||
			rep.New.Path != pastRep.New.Path ||
			rep.New.Version != pastRep.New.Version {
			changedPackages[importPath] = true
			whyChanged[importPath] = append(whyChanged[importPath], fmt.Sprintf("changed replace %s", importPath))
			continue
		}
	}
	// Mark removed replaces as changed.
	for importPath := range pastReplace {
		changedPackages[importPath] = true
		whyChanged[importPath] = append(whyChanged[importPath], fmt.Sprintf("removed replace %s", importPath))
	}

	changedCount := len(changedPackages)
	for {
	out:
		for _, pkg := range allPkgs {
			if changedPackages[pkg.ImportPath] && !why {
				continue
			}
			for _, importPath := range pkg.Imports {
				if changedPackages[importPath] {
					changedPackages[pkg.ImportPath] = true
					addReason(pkg.ImportPath, whyChanged[importPath])
					if !why {
						continue out
					}
				}
			}
			for _, importPath := range pkg.TestImports {
				if changedPackages[importPath] {
					changedPackages[pkg.ImportPath] = true
					addReason(pkg.ImportPath, whyChanged[importPath])
					if !why {
						continue out
					}
				}
			}
			for _, importPath := range pkg.XTestImports {
				if changedPackages[importPath] {
					changedPackages[pkg.ImportPath] = true
					addReason(pkg.ImportPath, whyChanged[importPath])
					if !why {
						continue out
					}
				}
			}
		}
		if changedCount == len(changedPackages) {
			break
		}
		changedCount = len(changedPackages)
	}

	for _, pkg := range pkgs {
		importPath := pkg.ImportPath
		if changedPackages[importPath] {
			if why {
				fmt.Printf("%s => %s\n", importPath, strings.Join(whyChanged[importPath], "\n	"))
			} else {
				fmt.Println(importPath)
			}
		}
	}
	os.Exit(0)
}
