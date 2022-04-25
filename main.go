package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/juju/collections/set"
	"github.com/juju/errors"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/hpidcock/gochanged/git"
	"github.com/hpidcock/gochanged/packages"
)

type ChangeLevel int

const (
	LevelNone ChangeLevel = iota
	LevelTestOnly
	LevelPackage
)

type Reasons struct {
	PackageReasons set.Strings
	TestReasons    set.Strings
}

func (r *Reasons) Set(level ChangeLevel) set.Strings {
	switch level {
	case LevelPackage:
		return r.PackageReasons
	case LevelTestOnly:
		return r.TestReasons
	default:
		panic("invalid ChangeLevel")
	}
}

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

	pkgs, extraPkgs, err := packages.ImportAll(build.Default, wd, packagesFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
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
	goModDir := path.Dir(commonModFile)
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

	changedDirectories := make(map[string]ChangeLevel)
	changedPackages := make(map[string]ChangeLevel)
	whyChanged := make(map[string]*Reasons)
	addReason := func(importPath string, level ChangeLevel, newReasons ...string) int {
		if changedPackages[importPath] < level {
			changedPackages[importPath] = level
		}
		if !why {
			return 0
		}
		reasons := whyChanged[importPath]
		if reasons == nil {
			reasons = &Reasons{
				PackageReasons: set.NewStrings(),
				TestReasons:    set.NewStrings(),
			}
			whyChanged[importPath] = reasons
		}
		s := reasons.Set(level)
		count := len(s)
		for _, reason := range newReasons {
			s.Add(reason)
		}
		return len(s) - count
	}
	importReasons := func(importPath string) []string {
		if reasons, ok := whyChanged[importPath]; ok && reasons != nil {
			return reasons.PackageReasons.Values()
		}
		return nil
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
			addReason(importPath, LevelPackage, fmt.Sprintf("new dep %s", importPath))
			continue
		}
		if dep.Mod.Version != pastVer.Version {
			addReason(importPath, LevelPackage, fmt.Sprintf("changed dep %s", importPath))
			continue
		}
	}
	// Mark different replaces as changed.
	for _, rep := range currentMod.Replace {
		importPath := rep.Old.Path
		pastRep, ok := pastReplace[importPath]
		if !ok {
			addReason(importPath, LevelPackage, fmt.Sprintf("new replace %s", importPath))
			continue
		}
		delete(pastReplace, importPath)
		if rep.Old.Version != pastRep.Old.Version ||
			rep.New.Path != pastRep.New.Path ||
			rep.New.Version != pastRep.New.Version {
			addReason(importPath, LevelPackage, fmt.Sprintf("changed replace %s", importPath))
			continue
		}
	}
	// Mark removed replaces as changed.
	for importPath := range pastReplace {
		addReason(importPath, LevelPackage, fmt.Sprintf("removed replace %s", importPath))
	}

	changedFiles, err := git.DiffNames(gitRoot, treeish)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	for _, file := range changedFiles {
		changeLevel := LevelPackage
		dir := path.Dir(file)
		if strings.Contains(strings.TrimPrefix(dir, goModDir), "testdata") {
			changeLevel = LevelTestOnly
		} else if strings.HasSuffix(file, "_test.go") {
			changeLevel = LevelTestOnly
		}
		if changeLevel > changedDirectories[dir] {
			changedDirectories[dir] = changeLevel
		}
	}

	allPkgs := append(append([]packages.Package(nil), pkgs...), extraPkgs...)
	for _, v := range pkgs {
		switch changedDirectories[path.Clean(v.Dir)] {
		case LevelPackage:
			addReason(v.ImportPath, LevelPackage, fmt.Sprintf("package changed %s", v.ImportPath))
		case LevelTestOnly:
			addReason(v.ImportPath, LevelTestOnly, fmt.Sprintf("tests changed %s", v.ImportPath))
		}
	}

	changedCount := len(changedPackages)
	more := 0
	for {
		more = 0
		for _, pkg := range allPkgs {
			if changedPackages[pkg.ImportPath] < LevelPackage && !why {
				continue
			}
			for _, importPath := range pkg.Imports {
				if changedPackages[importPath] >= LevelPackage {
					more += addReason(pkg.ImportPath, LevelPackage, importReasons(importPath)...)
				}
			}
			for _, importPath := range pkg.TestImports {
				if changedPackages[importPath] >= LevelPackage {
					more += addReason(pkg.ImportPath, LevelTestOnly, importReasons(importPath)...)
				}
			}
			for _, importPath := range pkg.XTestImports {
				if changedPackages[importPath] >= LevelPackage {
					more += addReason(pkg.ImportPath, LevelTestOnly, importReasons(importPath)...)
				}
			}
		}
		if changedCount == len(changedPackages) && more == 0 {
			break
		}
		changedCount = len(changedPackages)
	}

	for _, pkg := range pkgs {
		importPath := pkg.ImportPath
		if changedPackages[importPath] == LevelNone {
			continue
		}
		if !why {
			fmt.Println(importPath)
			continue
		}
		reasons := whyChanged[importPath]
		reasonList := append(reasons.PackageReasons.Values(), reasons.TestReasons.Values()...)
		sort.Strings(reasonList)
		fmt.Printf("%s => %s\n", importPath, strings.Join(reasonList, "\n	"))
	}
	os.Exit(0)
}
