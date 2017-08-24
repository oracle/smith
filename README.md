# `smith` - microcontainer builder #

![smith](https://github.com/oracle/smith/raw/master/smith.png
"smith")

## What is `smith`? ##

`smith` is a simple command line utility for building
[microcontainers](https://blogs.oracle.com/developers/the-microcontainer-manifesto)
from rpm packages or oci images.

## Principles of microcontainers ##

1. A microcontainer only contains the process to be run and its direct
   dependencies.

2. The microcontainer has files with no user ownership or special permissions
   beyond the executable bit.

3. The root filesystem of the container should be able to run read-only. All
   writes from the container should be into a directory called `/write`. Any
   unique config that an individual container instance will need should be
   placed into a directory called `/read`. Ephemeral files such as pid files
   can be written to `/run`.

## Building `smith` ##

[![wercker status](https://app.wercker.com/status/3795ec11f790da9b58d5acbdd1dafc9d/s/master "wercker status")](https://app.wercker.com/project/byKey/3795ec11f790da9b58d5acbdd1dafc9d)

Building can be done via the Makefile:

    make

Build dependencies:

    golang-bin

Go dependencies are vendored in the vendor directory.

## Runtime dependencies ##

To build from rpms, smith requires:

    mock

Although mock is used for rpm packaging, it can be installed on debian/ubuntu
if you are willing to be a little tricky. Specifically you need at least mock
1.2.  Version 1.1.X will not work because the -r flag does not support abspath
to the mock config file. Instructions for debian/ubuntu:

    sudo apt-get install createrepo yum
    # At the time of this writing the below package is suitable and available
    # for download. Your milage may vary and we suggest finding an official
    # debian mock package that is 1.2 or 1.3.
    wget http://ftp.debian.org/debian/pool/main/m/mock/mock_1.3.2-1_all.deb
    sudo dpkg -i mock_1.3.2-1_all.deb
    usermod -a -G mock <your_username>
    # rpm on debian has a patch to macros that messes up mock so undo it. Note
    # that updating your os will sometimes reset this file and you will have
    # to run this command again.
    sudo sed -i 's;%_dbpath\t.*;%_dbpath\t\t%{_var}/lib/rpm;g' /usr/lib/rpm/macros
    # on debian/ubuntu for some reason yum tries to install packages for
    # multiple archs, so it is necessary to update the yum.conf section in
    # default.cfg to prevent that. If you switch your default.cfg you may
    # have to do this again.
    sudo sed -i '/\[main\]/a multilib_policy=best' /etc/mock/default.cfg

## Installing `smith` ##

Installing can be done via the Makefile:

    sudo make install


## Installing `smith` using a Docker container ##

```bash
docker build -t smith .
```

## Using `smith` ##

To use smith, simply create a smith.yaml defining your container and run
`smith`. If you want to overlay additional files or symlinks, simply place them
into a directory called `rootfs` beside smith.yaml.

If you are building the same container multiple times without editing the
package line, the `-f` parameter will rebuild the container without
reinstalling the package.

## Build ##

To build a container with smith, create a smith.yaml file and invoke smith with no
parameters:

    mkdir cat
    cd cat

    cat >smith.yaml <<EOF
    package: coreutils
    paths:
    - /usr/bin/cat
    cmd:
    - /usr/bin/cat
    - /read/data
    EOF

    mkdir -p rootfs/read
    echo "Hello World!" >rootfs/read/data

    smith
    
## Build using a Docker container ##

Run the container mounting `smith.yaml` folder:

```bash
mkdir cat
cd cat

cat >smith.yaml <<EOF
package: coreutils
paths:
- /usr/bin/cat
cmd:
- /usr/bin/cat
- /read/data
EOF

mkdir -p rootfs/read
echo "Hello World!" >rootfs/read/data
```

Build `smith.yml`:

```bash
docker run -it --rm \
--privileged -v $PWD:/write \
-v cache:/var/cache \
-v mock:/var/lib/mock vishvananda/smith
```


You can also use an alias to run smith commands from your host:

```bash
smith(){
    docker run -it --rm \
    --privileged -v $PWD:/write \
    -v cache:/var/cache \
    -v mock:/var/lib/mock vishvananda/smith $@
}
```

Your image will be saved as image.tar.gz. You can change the name with a
parameter:

    smith -i cat.tar.gz

Smith has a few other options which can be viewed using "--help"

    smith --help

## Build Types ##

Smith can build from local rpm files or repositories. You can change the yum
config by modifying your /etc/mock/default.cfg.

Smith can also build directly from oci files downloaded via the download
command, or an oci directly from a docker repository. Simply specify either in
your smith.yaml as package, for example:

    package: https://registry-1.docker.io/library/fedora
    paths:
    - /usr/bin/cat
    cmd:
    - /usr/bin/cat
    - /read/data


To build Smith directly from oci, the Docker command is slightly different:

```bash
smith(){
    docker run -it --rm \
    -v $PWD:/write \
    -v tmp:/tmp vishvananda/smith $@
}
```

## Advanced Usage ##

For more detailed instructions on building containers, check out [How To Build
a Tiny Httpd
Container](https://hackernoon.com/how-to-build-a-tiny-httpd-container-ae622c37db39)

## Upload ##

You can upload your image to a docker repository:

    smith upload -r https://username:password@registry-1.docker.io/myrepo/cat -i cat.tar.gz

Images will be uploaded to the tag `latest`. You can specify an alternative tag
name to use appending it after a colon:

    smith upload -r https://registry-1.docker.io/myrepo/cat:newtag

It automatically uploads to registry-1.docker.io using docker media types.
Otherwise it tries to upload using oci media types.  If you want to upload to a
private docker v2 registry that doesn't support oci media types, you can use
the -d switch:

    smith upload -d -r https://myregistry.com/myrepo/cat -i cat.tar.gz

You can specify a tag name to upload to by appending it to the name

## Download ##

`smith` can also download existing images from docker repositories:

    smith download -r https://registry-1.docker.io/library/hello-world -i hello-world.tar.gz

It will convert these to tar.gz oci layouts. The `latest` tag will be
downloaded. To download an alternative tag, append it after a colon:

    smith download -r https://registry-1.docker.io/library/hello-world:othertag

## Contributing ##

Smith is an open source project. See [CONTRIBUTING](CONTRIBUTING.md) for
details.

Oracle gratefully acknowledges the contributions to smith that have been made
by the community.

## License ##

Copyright (c) 2017, Oracle and/or its affiliates. All rights reserved.

Smith is dual licensed under the Universal Permissive License 1.0 and the
Apache License 2.0.

See [LICENSE](LICENSE.txt) for more details.
