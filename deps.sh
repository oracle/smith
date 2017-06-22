#!/usr/bin/env bash
INTERP=`readelf -p .interp $1 | grep 0 | awk '{print $3}'`
echo $INTERP
$INTERP --list $1 | egrep "\s+\S+\s+=>\s+\S+\s+" | awk '{print $3}'
