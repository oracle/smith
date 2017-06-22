#!/usr/bin/env bash
#
# rpm-prep.sh: prepare source for rpm build

# name of the spec file and referenced tar.gz
NAME="smith"
# the prefix for version tags. This could be "v", "release-", or ""
# depending on the repository
VERPREFIX="v"
# the full glob for the version. This may need to be shortened if
# the repository has fewer parts to its version numbers.
VERGLOB=$VERPREFIX*.*.*

TREEISH=${1:-HEAD}
TARGET=$( realpath $( pwd ) )
HERE=$( realpath $( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd ) )
VERSION=$( cd $HERE && git describe --tags --match "$VERGLOB" | tr -d " \n" )
VERSION=${VERSION#$VERPREFIX}
REALV=${VERSION%%-*}
if [ "$REALV" != "$VERSION" ]; then
    COMMITS=${VERSION#*-}
    COMMITS=${COMMITS%%-*}
    VERSION=${REALV}.${COMMITS}.$( cd $HERE && git rev-parse --short HEAD | tr -d " \n" )
fi

echo "Generating spec file"
mkdir -p $TARGET/SPECS
sed s/@VERSION@/$VERSION/g $HERE/$NAME.spec > $TARGET/SPECS/$NAME.spec

echo "Generating tarball from $TREEISH"
mkdir -p $TARGET/SOURCES
(cd $HERE && git archive -o $TARGET/SOURCES/$NAME-$VERSION.tar.gz --prefix "$NAME-$VERSION/" $TREEISH)
