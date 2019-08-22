// Copyright (c) 2019, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package singularity

import (
	"errors"
	"fmt"
	"io"
	"os"

	ocitypes "github.com/containers/image/types"
	"github.com/sylabs/singularity/internal/pkg/build"
	"github.com/sylabs/singularity/internal/pkg/client/cache"
	ociclient "github.com/sylabs/singularity/internal/pkg/client/oci"
	"github.com/sylabs/singularity/internal/pkg/sylog"
	"github.com/sylabs/singularity/internal/pkg/util/uri"
	"github.com/sylabs/singularity/pkg/build/types"
	"gopkg.in/cheggaaa/pb.v1"
)

var (
	// ErrLibraryPullUnsigned indicates that the interactive portion of the pull was aborted.
	ErrLibraryPullUnsigned = errors.New("failed to verify container")
)

// printProgress is called to display progress bar while downloading image from library.
func printProgress(totalSize int64, r io.Reader, w io.Writer) error {
	bar := pb.New64(totalSize).SetUnits(pb.U_BYTES)
	bar.ShowTimeLeft = true
	bar.ShowSpeed = true

	// create proxy reader
	bodyProgress := bar.NewProxyReader(r)

	bar.Start()

	// Write the body to file
	_, err := io.Copy(w, bodyProgress)
	if err != nil {
		return err
	}

	bar.Finish()

	return nil
}

// OciPull will build a SIF image from the specified oci URI
func OciPull(imgCache *cache.Handle, name, imageURI, tmpDir string, ociAuth *ocitypes.DockerAuthConfig, noHTTPS bool) error {
	sysCtx := &ocitypes.SystemContext{
		OCIInsecureSkipTLSVerify:    noHTTPS,
		DockerInsecureSkipTLSVerify: noHTTPS,
		DockerAuthConfig:            ociAuth,
	}

	sum, err := ociclient.ImageSHA(imageURI, sysCtx)
	if err != nil {
		return fmt.Errorf("failed to get checksum for %s: %s", imageURI, err)
	}

	if imgCache.IsDisabled() {
		if err := convertDockerToSIF(imgCache, imageURI, name, tmpDir, noHTTPS, ociAuth); err != nil {
			return fmt.Errorf("while building SIF from layers: %v", err)
		}
	} else {
		imgName := uri.GetName(imageURI)
		cachedImgPath := imgCache.OciTempImage(sum, imgName)

		exists, err := imgCache.OciTempExists(sum, imgName)
		if err != nil {
			return fmt.Errorf("unable to check if %s exists: %s", imgName, err)
		}
		if !exists {
			sylog.Infof("Converting OCI blobs to SIF format")
			go interruptCleanup(imgName)

			if err := convertDockerToSIF(imgCache, imageURI, cachedImgPath, tmpDir, noHTTPS, ociAuth); err != nil {
				return fmt.Errorf("while building SIF from layers: %v", err)
			}
			sylog.Infof("Build complete: %s", name)
		}

		dstFile, err := openOutputImage(name)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		srcFile, err := os.Open(cachedImgPath)
		if err != nil {
			return fmt.Errorf("unable to open file for reading: %s: %v", name, err)
		}
		defer srcFile.Close()

		// Copy SIF from cache
		_, err = io.Copy(dstFile, srcFile)
		if err != nil {
			return fmt.Errorf("failed while copying files: %v", err)
		}
	}

	return nil
}

func convertDockerToSIF(imgCache *cache.Handle, image, cachedImgPath, tmpDir string, noHTTPS bool, authConf *ocitypes.DockerAuthConfig) error {
	if imgCache == nil {
		return fmt.Errorf("image cache is undefined")
	}

	b, err := build.NewBuild(
		image,
		build.Config{
			Dest:   cachedImgPath,
			Format: "sif",
			Opts: types.Options{
				TmpDir:           tmpDir,
				NoCache:          imgCache.IsDisabled(),
				NoTest:           true,
				NoHTTPS:          noHTTPS,
				DockerAuthConfig: authConf,
				ImgCache:         imgCache,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("unable to create new build: %v", err)
	}

	return b.Full()
}

func openOutputImage(path string) (*os.File, error) {
	// Perms are 755 *prior* to umask in order to allow image to be
	// executed with its leading shebang like a script
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return nil, fmt.Errorf("while opening destination file: %s", err)
	}

	return file, nil
}
