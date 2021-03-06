package main

import (
	"fmt"
	"os"
	"os/exec"

	rpc "github.com/mikkeloscar/aur"
	gopkg "github.com/mikkeloscar/gopkgbuild"
)

// Install handles package installs
func install(parser *arguments) error {
	aurs, repos, _ := packageSlices(parser.targets.toSlice())

	arguments := parser.copy()
	arguments.delArg("u", "sysupgrade")
	arguments.delArg("y", "refresh")
	arguments.targets = make(stringSet)
	arguments.addTarget(repos...)

	if len(repos) != 0 {
		err := passToPacman(arguments)
		if err != nil {
			fmt.Println("Error installing repo packages.")
		}
	}

	if len(aurs) != 0 {
		err := aurInstall(aurs, []string{})
		if err != nil {
			fmt.Println("Error installing aur packages.")
		}
	}
	return nil
}

// Install sends system commands to make and install a package from pkgName
func aurInstall(pkgName []string, flags []string) (err error) {
	q, err := rpc.Info(pkgName)
	if err != nil {
		return
	}

	if len(q) != len(pkgName) {
		fmt.Printf("Some packages from list\n%+v\n do not exist", pkgName)
	}

	var finalrm []string
	for _, i := range q {
		mrm, err := PkgInstall(&i, flags)
		if err != nil {
			fmt.Println("Error installing", i.Name, ":", err)
		}
		finalrm = append(finalrm, mrm...)
	}

	if len(finalrm) != 0 {
		err = removeMakeDeps(finalrm)
	}

	return err
}

func setupPackageSpace(a *rpc.Pkg) (dir string, pkgbuild *gopkg.PKGBUILD, err error) {
	dir = config.BuildDir + a.PackageBase + "/"

	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		if !continueTask("Directory exists. Clean Build?", "yY") {
			_ = os.RemoveAll(config.BuildDir + a.PackageBase)
		}
	}

	if err = downloadAndUnpack(baseURL+a.URLPath, config.BuildDir, false); err != nil {
		return
	}

	if !continueTask("Edit PKGBUILD?", "yY") {
		editcmd := exec.Command(editor(), dir+"PKGBUILD")
		editcmd.Stdin, editcmd.Stdout, editcmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		editcmd.Run()
	}

	pkgbuild, err = gopkg.ParseSRCINFO(dir + ".SRCINFO")
	if err == nil {
		for _, pkgsource := range pkgbuild.Source {
			owner, repo := parseSource(pkgsource)
			if owner != "" && repo != "" {
				err = branchInfo(a.Name, owner, repo)
				if err != nil {
					fmt.Println(err)
				}
			}
		}
	}
	return
}

// PkgInstall handles install from Info Result.
func PkgInstall(a *rpc.Pkg, flags []string) (finalmdeps []string, err error) {
	fmt.Printf("\x1b[1;32m==> Installing\x1b[33m %s\x1b[0m\n", a.Name)
	if a.Maintainer == "" {
		fmt.Println("\x1b[1;31;40m==> Warning:\x1b[0;;40m This package is orphaned.\x1b[0m")
	}

	dir, _, err := setupPackageSpace(a)
	if err != nil {
		return
	}

	if specialDBsauce {
		return
	}

	runDeps, makeDeps, err := pkgDependencies(a)
	if err != nil {
		return
	}

	repoDeps := append(runDeps[0], makeDeps[0]...)
	aurDeps := append(runDeps[1], makeDeps[1]...)
	finalmdeps = append(finalmdeps, makeDeps[0]...)
	finalmdeps = append(finalmdeps, makeDeps[1]...)

	if len(aurDeps) != 0 || len(repoDeps) != 0 {
		if !continueTask("Continue?", "nN") {
			return finalmdeps, fmt.Errorf("user did not like the dependencies")
		}
	}

	aurQ, _ := rpc.Info(aurDeps)
	if len(aurQ) != len(aurDeps) {
		(aurQuery)(aurQ).missingPackage(aurDeps)
		if !continueTask("Continue?", "nN") {
			return finalmdeps, fmt.Errorf("unable to install dependencies")
		}
	}

	arguments := makeArguments()
	arguments.addArg("S", "asdeps", "noconfirm")
	arguments.addTarget(repoDeps...)

	var depArgs []string
	if config.NoConfirm {
		depArgs = []string{"asdeps", "noconfirm"}
	} else {
		depArgs = []string{"asdeps"}
	}
	
	// Repo dependencies
	if len(repoDeps) != 0 {
		errR := passToPacman(arguments)
		if errR != nil {
			return finalmdeps, errR
		}
	}

	// Handle AUR dependencies
	for _, dep := range aurQ {
		finalmdepsR, errA := PkgInstall(&dep, depArgs)
		finalmdeps = append(finalmdeps, finalmdepsR...)

		if errA != nil {
			cleanRemove(repoDeps)
			cleanRemove(aurDeps)
			return finalmdeps, errA
		}
	}

	flags = append(flags, "-sri")
	err = passToMakepkg(dir, flags...)
	return
}
