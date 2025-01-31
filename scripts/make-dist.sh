#!/bin/sh -
# Copyright (c) Sylabs Inc. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be found
# in the LICENSE file.
set -e

package_name=singularity

if [ ! -f $package_name.spec ]; then
    echo "Run this from the top of the source tree after mconfig" >&2
    exit 1
fi

version=`scripts/get-version`

echo " DIST setup VERSION: ${version}"
echo "${version}" > VERSION
rmfiles="VERSION"
tarball="${package_name}-${version}.tar.gz"
echo " DIST create tarball: $tarball"
rm -f $tarball
pathtop="$package_name"
ln -sf .. builddir/$pathtop
rmfiles="$rmfiles builddir/$pathtop"
trap "rm -f $rmfiles" 0

# modules should have been vendored using the correct version of the Go
# tool, so we expect to find a vendor directory. Bail out if there isn't
# one.
if test ! -d vendor ; then
    echo 'E: vendor directory not found. Abort.'
    exit 1
fi

(echo VERSION; echo $package_name.spec; echo vendor; git ls-files) | \
    sed "s,^,$pathtop/," |
    tar -C builddir -T - -czf $tarball
