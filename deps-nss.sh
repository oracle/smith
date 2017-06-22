#!/usr/bin/env bash
INTERP=`readelf -p .interp $1 | grep 0 | awk '{print $3}'`
SOS=`$INTERP --list $1 | egrep "\s+\S+\s+=>\s+\S+\s+" | awk '{print $3}'`

LIBC=`echo $SOS | grep 'libc\.so\.6'`
PATH=${LIBC%/*}

echo $INTERP
echo $SOS
echo $PATH/libnss_dns.so.2
echo $PATH/libnss_files.so.2
echo $PATH/libnss_compat.so.2
