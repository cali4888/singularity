// Copyright (c) 2018, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package libexec

import (
	"github.com/sylabs/singularity/internal/pkg/build"
	"github.com/sylabs/singularity/internal/pkg/sylog"
	"github.com/sylabs/singularity/pkg/build/types"
	library "github.com/sylabs/singularity/pkg/client/library"
	shub "github.com/sylabs/singularity/pkg/client/shub"
)

// PullLibraryImage is the function that is responsible for pulling an image from a Sylabs library.
func PullLibraryImage(image, libraryRef, libraryURL string, force bool, authToken string) {
	err := library.DownloadImage(image, libraryRef, libraryURL, force, authToken)
	if err != nil {
		sylog.Fatalf("%v\n", err)
	}
}

// PullShubImage is the function that is responsible for pulling an image from a Singularity Hub.
func PullShubImage(filePath, shubRef string, force bool) {
	err := shub.DownloadImage(filePath, shubRef, force)
	if err != nil {
		sylog.Fatalf("%v\n", err)
	}
}

// PullOciImage pulls an OCI image to a sif
func PullOciImage(path, uri string, force, noHTTPS bool) {
	b, err := build.NewBuild(uri, path, "sif", "", "", types.Options{Force: force, NoHTTPS: noHTTPS})
	if err != nil {
		sylog.Fatalf("Unable to pull %v: %v", uri, err)
	}

	if err := b.Full(); err != nil {
		sylog.Fatalf("Unable to pull %v: %v", uri, err)
	}
}
