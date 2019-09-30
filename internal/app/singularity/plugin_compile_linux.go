// Copyright (c) 2018-2019, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package singularity

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"runtime"
	"strings"

	uuid "github.com/satori/go.uuid"
	"github.com/sylabs/sif/pkg/sif"
	"github.com/sylabs/singularity/internal/pkg/buildcfg"
	"github.com/sylabs/singularity/internal/pkg/sylog"
	pluginapi "github.com/sylabs/singularity/pkg/plugin"
)

// getSingularitySrcDir returns the source directory for singularity.
func getSingularitySrcDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	canary := filepath.Join(dir, "cmd", "singularity", "cli.go")

	switch _, err = os.Stat(canary); {
	case os.IsNotExist(err):
		return "", fmt.Errorf("cannot find %q", canary)

	case err != nil:
		return "", fmt.Errorf("unexpected error while looking for %q: %s", canary, err)

	default:
		return dir, nil
	}
}

// CompilePlugin compiles a plugin. It takes as input: sourceDir, the path to the
// plugin's source code directory; and destSif, the path to the intended final
// location of the plugin SIF file.
func CompilePlugin(sourceDir, mainPkg, destSif, tmpDir string) error {
	pluginDir, err := ioutil.TempDir(tmpDir, filepath.Base(sourceDir))
	if err != nil {
		return fmt.Errorf("could not create temp dir: %v", err)
	}
	defer os.RemoveAll(pluginDir)

	pluginPath, err := buildPlugin(sourceDir, mainPkg, pluginDir)
	if err != nil {
		return fmt.Errorf("while building plugin .so: %v", err)
	}

	manifestPath, err := generateManifest(pluginPath, pluginDir)
	if err != nil {
		return fmt.Errorf("while generating plugin manifest: %s", err)
	}

	gzPath, err := compressDir(sourceDir, pluginDir)
	if err != nil {
		return fmt.Errorf("while generating source gz: %s", err)
	}

	// convert the built plugin object into a sif
	if err := makeSIF(destSif, pluginPath, manifestPath, gzPath); err != nil {
		return fmt.Errorf("while making sif file: %s", err)
	}

	return nil
}

// buildPlugin takes sourceDir which is the string path the host which contains the source code of
// the plugin. buildPlugin returns the path to the built file, along with an error.
//
// This function essentially runs the `go build -buildmode=plugin [...]` command
func buildPlugin(sourceDir, mainPkg, destDir string) (string, error) {
	workpath, err := getSingularitySrcDir()
	if err != nil {
		return "", errors.New("singularity source directory not found")
	}

	out := filepath.Join(destDir, "plugin.so")
	args := []string{
		"build",
		"-o", out,
		"-buildmode=plugin",
		"-mod=vendor",
		"-tags", buildcfg.GO_BUILD_TAGS,
		fmt.Sprintf("-gcflags=all=-trimpath=%s", workpath),
		fmt.Sprintf("-asmflags=all=-trimpath=%s", workpath),
		filepath.Join(sourceDir, mainPkg),
	}

	sylog.Debugf("Running: go %s", strings.Join(args, " "))

	buildcmd := exec.Command("go", args...)
	buildcmd.Dir = workpath
	buildcmd.Stderr = os.Stderr
	buildcmd.Stdout = os.Stdout
	buildcmd.Env = append(os.Environ(), "GO111MODULE=on")

	return out, buildcmd.Run()
}

// generateManifest takes the path to the plugin source, sourceDir, and generates
// its corresponding manifest file.
func generateManifest(pluginPath, destDir string) (string, error) {
	sylog.Debugf("Opening %q", pluginPath)
	pluginPointer, err := plugin.Open(pluginPath)
	if err != nil {
		return "", err
	}

	sym, err := pluginPointer.Lookup(pluginapi.PluginSymbol)
	if err != nil {
		return "", err
	}

	p, ok := sym.(*pluginapi.Plugin)
	if !ok {
		return "", fmt.Errorf("symbol \"Plugin\" not of type Plugin")
	}

	manifest, err := json.Marshal(p.Manifest)
	if err != nil {
		return "", fmt.Errorf("could not marshal manifest to json: %v", err)
	}

	out := filepath.Join(destDir, "manifest.json")
	if err := ioutil.WriteFile(out, manifest, 0644); err != nil {
		return "", fmt.Errorf("could not write manifest: %v", err)
	}
	return out, nil
}

// makeSIF takes in two arguments: sourceDir, the path to the plugin source directory;
// and sifPath, the path to the final .sif file which is ready to be used.
func makeSIF(sifPath, pluginPath, manifestPath, gzPath string) error {
	plCreateInfo := sif.CreateInfo{
		Pathname:   sifPath,
		Launchstr:  sif.HdrLaunch,
		Sifversion: sif.HdrVersion,
		ID:         uuid.NewV4(),
	}

	plObjInput, err := getPluginObjDescr(pluginPath)
	if err != nil {
		return err
	}
	if fp, ok := plObjInput.Fp.(io.Closer); ok {
		defer fp.Close()
	}
	plCreateInfo.InputDescr = append(plCreateInfo.InputDescr, plObjInput)

	plManifestInput, err := getPluginManifestDescr(manifestPath)
	if err != nil {
		return err
	}
	if fp, ok := plManifestInput.Fp.(io.Closer); ok {
		defer fp.Close()
	}
	plCreateInfo.InputDescr = append(plCreateInfo.InputDescr, plManifestInput)

	plGzInput, err := getSourceGzObjDescr(gzPath)
	if err != nil {
		return err
	}
	if fp, ok := plGzInput.Fp.(io.Closer); ok {
		defer fp.Close()
	}
	plCreateInfo.InputDescr = append(plCreateInfo.InputDescr, plGzInput)

	os.RemoveAll(sifPath)

	// create sif file
	if _, err := sif.CreateContainer(plCreateInfo); err != nil {
		return fmt.Errorf("while creating sif file: %s", err)
	}

	return nil
}

func compressDir(sourcePath, destDir string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("Could not get working dir: %s", err)
	}
	sourcePath, err = filepath.Rel(wd, sourcePath)
	if err != nil {
		return "", fmt.Errorf("Could not make source path relative: %s", err)
	}

	destFilePath := filepath.Join(destDir, "plugin.tar.gz")
	destFile, err := os.Create(destFilePath)
	if err != nil {
		return "", fmt.Errorf("could not create plugin gzip file: %s", err)
	}
	defer destFile.Close()

	gzw := gzip.NewWriter(destFile)
	defer gzw.Close()
	trw := tar.NewWriter(gzw)
	defer trw.Close()

	err = filepath.Walk(sourcePath, func(file string, fi os.FileInfo, err error) error {
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}
		header.Name = file

		if err := trw.WriteHeader(header); err != nil {
			return err
		}

		if fi.IsDir() {
			return nil
		}

		data, err := os.Open(file)
		if err != nil {
			return err
		}
		defer data.Close()
		if _, err := io.Copy(trw, data); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("could not compress dir: %s", err)
	}

	return destFilePath, nil
}

// getPluginObjDescr returns a sif.DescriptorInput which contains the raw
// data of the .so file.
//
// Datatype: sif.DataPartition
// Fstype:   sif.FsRaw
// Parttype: sif.PartData
func getPluginObjDescr(objPath string) (sif.DescriptorInput, error) {
	var err error

	objInput := sif.DescriptorInput{
		Datatype: sif.DataPartition,
		Groupid:  sif.DescrDefaultGroup,
		Link:     sif.DescrUnusedLink,
		Fname:    objPath,
	}

	// open plugin object file
	fp, err := os.Open(objInput.Fname)
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while opening plugin object file %s: %s", objInput.Fname, err)
	}

	// stat file to obtain size
	fstat, err := fp.Stat()
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while calling stat on plugin object file %s: %s", objInput.Fname, err)
	}

	objInput.Fp = fp
	objInput.Size = fstat.Size()

	// populate objInput.Extra with appropriate Fstype & Parttype
	err = objInput.SetPartExtra(sif.FsRaw, sif.PartData, sif.GetSIFArch(runtime.GOARCH))
	if err != nil {
		return sif.DescriptorInput{}, err
	}

	return objInput, nil
}

func getSourceGzObjDescr(objPath string) (sif.DescriptorInput, error) {
	var err error
	objInput := sif.DescriptorInput{
		Datatype: sif.DataGeneric,
		Groupid:  sif.DescrDefaultGroup,
		Link:     sif.DescrUnusedLink,
		Fname:    objPath,
	}

	// open plugin object file
	fp, err := os.Open(objInput.Fname)
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while opening source gz file %s: %s", objInput.Fname, err)
	}

	// stat file to obtain size
	fstat, err := fp.Stat()
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while calling stat on source gz file %s: %s", objInput.Fname, err)
	}

	objInput.Fp = fp
	objInput.Size = fstat.Size()
	return objInput, nil
}

// getPluginManifestDescr returns a sif.DescriptorInput which contains the manifest
// in JSON form. Grabbing the Manifest is done by loading the .so using the plugin
// package, which is performed inside the container during buildPlugin() function
//
// Datatype: sif.DataGenericJSON
func getPluginManifestDescr(manifestPath string) (sif.DescriptorInput, error) {
	manifestInput := sif.DescriptorInput{
		Datatype: sif.DataGenericJSON,
		Groupid:  sif.DescrDefaultGroup,
		Link:     sif.DescrUnusedLink,
		Fname:    manifestPath,
	}

	// open plugin object file
	fp, err := os.Open(manifestInput.Fname)
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while opening manifest file %s: %s", manifestInput.Fname, err)
	}

	// stat file to obtain size
	fstat, err := fp.Stat()
	if err != nil {
		return sif.DescriptorInput{}, fmt.Errorf("while calling stat on manifest file %s: %s", manifestInput.Fname, err)
	}

	manifestInput.Fp = fp
	manifestInput.Size = fstat.Size()
	return manifestInput, nil
}
