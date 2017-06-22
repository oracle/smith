#!/usr/bin/env bash
#
# rpm-clean.sh: prepare source for rpm build
#

set -x
TARGET=$( realpath $( pwd ) )
HERE=$( realpath $( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd ) )
echo "Removing spec and sources"
rm -rf $TARGET/SOURCES $TARGET/SPECS
echo "Removing config.yaml"
rm -f $TARGET/config.yaml
