#!/bin/bash
#
# Simple script to build matterbridge using go version 1.24 and the latest whatsmeow release.
#
# Run it from the matterbridge dir after doing a fresh clone.
#
# TODO: handle more errors, make more portable
#
# tested on Debian 12 using go 1.24.13, working as of 11 June 2026
#


# Utility functions

# print a complaint on stderr

warn () {
    echo "$0:" "$@" >&2
}

# bail out

die () {
    rc=$1
    shift
    warn "$@"
    exit $rc
}

# let's make sure we even need to do anything here
GOVERSION=`go version | fgrep 1.24`

if [ -z "$GOVERSION" ]; then echo $GOVERSION; die 1 "Wrong go version in path"; fi


mkdir -p oldlibs || die 1 "mkdir oldlibs failed"

cd oldlibs

git clone https://github.com/tulir/whatsmeow/ || die 1 "cloning tulir/whatsmeow repo"

sed -i '3s/.*/go 1.24.0/' whatsmeow/go.mod || die 1 "writing whatsmeow/go.mod"
sed -i '5s/.*/toolchain go1.24.0/' whatsmeow/go.mod || die 1 "writing whatsmeow/go.mod"

cat >> whatsmeow/go.mod << _EOF_ || die 1 "writing whatsmeow/go.mod"

replace go.mau.fi/libsignal => ../libsignal
replace go.mau.fi/util => ../util
replace golang.org/x/net => ../net
replace golang.org/x/crypto => ../crypto
replace golang.org/x/sync => ../sync
replace golang.org/x/exp => ../exp
replace golang.org/x/sys => ../sys
replace golang.org/x/text => ../text

_EOF_

git clone https://github.com/mautrix/go-util/ || die 1 "cloning mautrix/go-util repo"

mv go-util util || die 1 "renaming go-util to util"

sed -i '3s/.*/go 1.24.0/' util/go.mod || die 1 "writing util/go.mod"
sed -i '5s/.*/toolchain go1.24.0/' util/go.mod || die 1 "writing util/go.mod"

cat >> util/go.mod << _EOF_ || die 1 "writing util/go.mod"

replace golang.org/x/exp => ../exp
replace golang.org/x/mod => ../mod
replace golang.org/x/text => ../text
replace golang.org/x/net => ../net
replace golang.org/x/sys => ../sys

_EOF_

git clone https://github.com/golang/mod/ || die 1 "cloning x/mod repo"
cd mod && git reset --hard 27761a2 || die 1 "reverting to last commit before go 1.25 requirement in x/mod"
cd ..

git clone https://github.com/golang/exp/ || die 1 "cloning x/exp repo"
cd exp && git reset --hard 944ab1f || die 1 "reverting to last commit before go 1.25 requirement in x/exp"
cd ..

git clone https://github.com/golang/text || die 1 "cloning x/text repo"
cd text && git reset --hard 817fba9 || die 1 "reverting to last commit before go 1.25 requirement in x/text"
cd ..

git clone https://github.com/golang/net || die 1 "cloning x/net repo"
cd net && git reset --hard 0b37bdf || die 1 "reverting to last commit before go 1.25 requirement in x/net"
cd ..

git clone https://github.com/golang/sys || die 1 "cloning x/sys repo"
cd sys && git reset --hard fc646e4 || die 1 "reverting to last commit before go 1.25 requirement in x/sys"
cd ..

git clone https://github.com/tulir/libsignal-protocol-go/ || die 1 "cloning libsignal repo"
mv libsignal-protocol-go libsignal || die 1 "renaming libsignal-protocol-go to libsignal"
cd libsignal && git reset --hard 188d843 || die 1 "reverting to last commit before go 1.25 requirement in libsignal"
sed -i '5s/.*/toolchain go1.24.0/' ./go.mod || die 1 "writing libsignal/go.mod"
cd ..

git clone https://github.com/golang/crypto/ || die 1 "cloning x/crypto repo"
cd crypto && git reset --hard 2f26647 || die 1 "reverting to last commit before go 1.25 requirement in x/crypto"
cd ..

git clone https://github.com/golang/sync || die 1 "cloning x/sync repo"
cd sync && git reset --hard 2a180e2 || die 1 "reverting to last commit before go 1.25 requirement in x/sync"
cd ..

for dir in `ls`; do echo -n "Tidying $dir..."; cd $dir; go mod tidy; cd ..; echo " done"; done

cd ..

# Now, we're back in the matterbridge main dir

cat >> go.mod << _EOF_ || die 1 "writing go.mod in matterbridge base dir"

replace go.mau.fi/whatsmeow => ./oldlibs/whatsmeow
replace go.mau.fi/util => ./oldlibs/util
replace golang.org/x/sync => ./oldlibs/sync
replace go.mau.fi/libsignal => ./oldlibs/libsignal
replace golang.org/x/mod => ./oldlibs/mod
replace golang.org/x/crypto => ./oldlibs/crypto
replace golang.org/x/exp => ./oldlibs/exp
replace golang.org/x/net => ./oldlibs/net
replace golang.org/x/sys => ./oldlibs/sys
replace golang.org/x/text => ./oldlibs/text

_EOF_

echo -n "Tidying matterbridge..."
go mod tidy

echo " done!"
echo
echo "If there were no issues shown above, it's time to try a build!"

#CGO_ENABLED=0 go build -tags goolm

exit 0
