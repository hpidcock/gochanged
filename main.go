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

	"github.com/dominikbraun/graph"
	"github.com/juju/errors"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/hpidcock/gochanged/git"
	"github.com/hpidcock/gochanged/packages"
)

type P struct {
	Path    string
	Test    bool
	Changed bool
}

func main() {
	treeish := ""
	why := false
	flag.StringVar(&treeish, "branch", "", "git branch or treeish to diff against")
	flag.BoolVar(&why, "why", false, "explain why each package changed")
	flag.Parse()
	packagesFilter := flag.Args()
	if len(packagesFilter) == 0 {
		packagesFilter = []string{"./..."}
	}

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
		fmt.Fprintf(os.Stderr, "%s is not under git root %s", commonModFile, gitRoot)
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

	changedDirectories := make(map[string]bool)
	changedDirectoriesTest := make(map[string]bool)
	changedPackages := make(map[string]bool)
	changedTestPackages := make(map[string]bool)
	whyChanged := make(map[string][]string)
	whyChangedTests := make(map[string][]string)

	if workspace, err := packages.Workspace(build.Default, wd); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	} else if workspace != "" {
		for _, pkg := range packagesFilter {
			if why {
				fmt.Fprintf(os.Stderr, "%s => workspace mode", pkg)
			} else {
				fmt.Println(pkg)
			}
		}
		os.Exit(0)
	}

	if currentMod.Go.Version != pastMod.Go.Version {
		for _, pkg := range packagesFilter {
			if why {
				fmt.Fprintf(os.Stderr, "%s => go mod version changed", pkg)
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

	changedFiles, err := git.DiffNames(gitRoot, treeish)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	for _, file := range changedFiles {
		dir := path.Dir(file)
		if strings.Contains(strings.TrimPrefix(dir, goModDir), "testdata") {
			changedDirectoriesTest[dir] = true
		} else if strings.HasSuffix(file, "_test.go") {
			changedDirectoriesTest[dir] = true
		} else {
			changedDirectories[dir] = true
		}
	}

	allPkgs := append(append([]packages.Package(nil), pkgs...), extraPkgs...)
	for _, v := range pkgs {
		dir := path.Clean(v.Dir)
		if changedDirectories[dir] {
			changedPackages[v.ImportPath] = true
			whyChanged[v.ImportPath] = append(whyChanged[v.ImportPath], fmt.Sprintf("package changed %s", v.ImportPath))
		}
		if changedDirectoriesTest[dir] {
			changedTestPackages[v.ImportPath] = true
			whyChangedTests[v.ImportPath] = append(whyChangedTests[v.ImportPath], fmt.Sprintf("tests changed %s", v.ImportPath))
		}
	}

	g := graph.New(graph.StringHash, graph.Directed(), graph.Acyclic())
	for _, pkg := range allPkgs {
		err := g.AddVertex(pkg.ImportPath)
		if err != nil {
			panic(err)
		}
	}

	for _, pkg := range allPkgs {
		for _, importPath := range pkg.Imports {
			g.AddEdge(pkg.ImportPath, importPath)
		}
	}

	needsTest := make(map[string]bool)
	for _, pkg := range allPkgs {
		if !changedPackages[pkg.ImportPath] {
			continue
		}
		needsTest[pkg.ImportPath] = true
		ReverseDFS(g, pkg.ImportPath, func(importPath string) bool {
			needsTest[importPath] = true
			return false
		})
	}

	extraNeedsTest := make(map[string]bool)
nextPackage:
	for _, pkg := range allPkgs {
		for _, importPath := range pkg.TestImports {
			if needsTest[importPath] {
				extraNeedsTest[pkg.ImportPath] = true
			}
		}
		for _, importPath := range pkg.XTestImports {
			if needsTest[importPath] {
				extraNeedsTest[pkg.ImportPath] = true
				continue nextPackage
			}
		}
	}

	for importPath := range extraNeedsTest {
		needsTest[importPath] = true
		whyChangedTests[importPath] = append(whyChangedTests[importPath], "test deps changed")
	}

	for _, pkg := range pkgs {
		importPath := pkg.ImportPath
		if !needsTest[importPath] {
			continue
		}
		if !why {
			fmt.Println(importPath)
			continue
		}
		reasonList := append([]string(nil), whyChanged[importPath]...)
		reasonList = append(reasonList, whyChangedTests[importPath]...)
		graph.BFS(g, importPath, func(ip string) bool {
			reasonList = append(reasonList, whyChanged[ip]...)
			return false
		})
		sort.Strings(reasonList)
		fmt.Fprintf(os.Stderr, "%s => %s\n", importPath, strings.Join(reasonList, "\n	"))
	}
	os.Exit(0)
}

// Copied from github.com/dominikbraun/graph (with modifications) which is licensed under Apache License.
func ReverseDFS[K comparable, T any](g graph.Graph[K, T], start K, visit func(K) bool) error {
	predMap, err := g.PredecessorMap()
	if err != nil {
		return fmt.Errorf("could not get adjacency map: %w", err)
	}

	if _, ok := predMap[start]; !ok {
		return fmt.Errorf("could not find start vertex with hash %v", start)
	}

	stack := make([]K, 0)
	visited := make(map[K]bool)

	stack = append(stack, start)

	for len(stack) > 0 {
		currentHash := stack[len(stack)-1]

		stack = stack[:len(stack)-1]

		if _, ok := visited[currentHash]; !ok {
			// Stop traversing the graph if the visit function returns true.
			if stop := visit(currentHash); stop {
				break
			}
			visited[currentHash] = true

			for adjacency := range predMap[currentHash] {
				stack = append(stack, adjacency)
			}
		}
	}

	return nil
}
