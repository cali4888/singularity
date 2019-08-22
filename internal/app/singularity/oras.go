package singularity

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/sylabs/singularity/internal/pkg/oras"
	"github.com/sylabs/singularity/internal/pkg/sylog"
	"github.com/sylabs/singularity/internal/pkg/util/uri"

	"github.com/containers/image/types"
	"github.com/sylabs/singularity/internal/pkg/client/cache"
)

type Oras struct {
	cache   *cache.Handle
	ociAuth *types.DockerAuthConfig
}

func NewOras(c *cache.Handle, auth *types.DockerAuthConfig) *Oras {
	return &Oras{cache: c, ociAuth: auth}
}

// Pull will download the image specified by the provided oci reference and store
// it at the location specified by file, it will use credentials if supplied.
func (o *Oras) Pull(ctx context.Context, from, to string) error {
	sum, err := oras.ImageSHA(from, o.ociAuth)
	if err != nil {
		return fmt.Errorf("failed to get checksum for %s: %s", from, err)
	}

	imageName := uri.GetName("oras:" + from)

	cacheImagePath := o.cache.OrasImage(sum, imageName)
	exists, err := o.cache.OrasImageExists(sum, imageName)
	if err == cache.ErrBadChecksum {
		sylog.Warningf("Removing cached image: %s: cache could be corrupted", cacheImagePath)
		err := os.Remove(cacheImagePath)
		if err != nil {
			return fmt.Errorf("unable to remove corrupted cache: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to check if %s exists: %v", cacheImagePath, err)
	}

	if !exists {
		sylog.Infof("Downloading image with ORAS")
		go interruptCleanup(cacheImagePath)

		if err := oras.DownloadImage(cacheImagePath, from, o.ociAuth); err != nil {
			return fmt.Errorf("unable to Download Image: %v", err)
		}

		if cacheFileHash, err := oras.ImageHash(cacheImagePath); err != nil {
			return fmt.Errorf("error getting ImageHash: %v", err)
		} else if cacheFileHash != sum {
			return fmt.Errorf("cached file hash(%s) and expected hash(%s) does not match", cacheFileHash, sum)
		}
	} else {
		sylog.Infof("Using cached image")
	}

	dstFile, err := openOutputImage(to)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	srcFile, err := os.Open(cacheImagePath)
	if err != nil {
		return fmt.Errorf("while opening cached image: %v", err)
	}
	defer srcFile.Close()

	// Copy SIF from cache
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("while copying image from cache: %v", err)
	}

	sylog.Infof("Pull complete: %s\n", to)

	return nil
}
