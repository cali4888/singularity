package singularity

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/sylabs/singularity/internal/pkg/sylog"
	"github.com/sylabs/singularity/internal/pkg/util/uri"
	shub "github.com/sylabs/singularity/pkg/client/shub"

	"github.com/sylabs/singularity/internal/pkg/client/cache"
)

type Shub struct {
	cache   *cache.Handle
	noHttps bool
}

func NewShub(c *cache.Handle, noHttps bool) *Shub {
	return &Shub{cache: c, noHttps: noHttps}
}

// Shub will download a image from shub, and cache it. Next time
// that container is downloaded this will just use that cached image.
func (s *Shub) Pull(ctx context.Context, from, to string) error {
	shubURI, err := shub.ShubParseReference(from)
	if err != nil {
		return fmt.Errorf("failed to parse shub uri: %s", err)
	}

	// Get the image manifest
	manifest, err := shub.GetManifest(shubURI, s.noHttps)
	if err != nil {
		return fmt.Errorf("failed to get manifest for: %s: %s", from, err)
	}

	imageName := uri.GetName(from)
	imagePath := s.cache.ShubImage(manifest.Commit, imageName)

	if s.cache.IsDisabled() {
		// Dont use cached image
		if err := shub.DownloadImage(manifest, to, from, true, s.noHttps); err != nil {
			return err
		}
	} else {
		exists, err := s.cache.ShubImageExists(manifest.Commit, imageName)
		if err != nil {
			return fmt.Errorf("unable to check if %v exists: %v", imagePath, err)
		}
		if !exists {
			sylog.Infof("Downloading shub image")
			go interruptCleanup(imagePath)

			err := shub.DownloadImage(manifest, imagePath, from, true, s.noHttps)
			if err != nil {
				return err
			}
		} else {
			sylog.Infof("Use image from cache")
		}

		dstFile, err := openOutputImage(to)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		srcFile, err := os.Open(imagePath)
		if err != nil {
			return fmt.Errorf("while opening cached image: %v", err)
		}
		defer srcFile.Close()

		// Copy image from cache
		_, err = io.Copy(dstFile, srcFile)
		if err != nil {
			return fmt.Errorf("while copying image from cache: %v", err)
		}
	}

	return nil
}
