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

## Building and Running `smith` ##

You can build and run `smith` either as:
- A Docker image
- A Binary

Both methods are described below, but __the Docker route is recommended__ as the simplest and easiest option.

### Docker based `smith` ###

#### Dependencies ####
- Docker

#### Method #### 
1.  Clone `smith`:

`git clone https://github.com/oracle/smith.git`

2.  Build `smith` Docker image using the Dockerfile provided, optionally adding your own docker-repo-id to the tag:

`sudo docker build -t [<docker-repo-id>/]smith .`

3.  Set up an alias (or script) to run `smith` from the command line:
```
smith(){
    sudo docker run -it --rm \
    --privileged -v $PWD:/write \
    -v cache:/var/cache \
    -v /tmp:/tmp \
    -v mock:/var/lib/mock [<docker-repo-id>/]smith $@
}
```
You should now be able to start building microcontainers (see below).
 
### Binary `smith` ###


[![wercker status](https://app.wercker.com/status/3795ec11f790da9b58d5acbdd1dafc9d/s/master "wercker status")](https://app.wercker.com/project/byKey/3795ec11f790da9b58d5acbdd1dafc9d)

Building can be done via the Makefile:

    make

#### Dependencies ####
##### Build #####
- Docker
- Go

To install go run `sudo yum install golang-bin` or `sudo apt install golang-go` as appropriate

Go dependencies are vendored in the vendor directory.

##### Runtime #####

To build from RPMs, `smith` requires:

- mock

mock can have issues with non - RPM distros.

**If you have problems installing or running `smith` natively on a non - RPM distro, best advice is to build it and run it in a Docker container (see above)**

mock can be installed on Debian/Ubuntu with some extra care (see below).  Specifically you need at least mock
1.2.  Version 1.1.X will not work because the -r flag does not support abspath to the mock config file.
Be aware that your `smith` builds may still fail.

Debian/Ubuntu specific instructions (_Here be Dragons_):

```sudo apt install mock createrepo yum```

```
# Fedora rawhide chroot (which mock uses by default) does not play well with
# Debian, so point /etc/mock/default.cfg to EPEL 7 (6 on Ubuntu):
sudo ln -s /etc/mock/epel-7-x86_64.cfg /etc/mock/default.cfg 
```
```
# rpm on Debian has a patch to macros that messes up mock so undo it. Note
# that updating your os will sometimes reset this file and you will have
# to run this command again.
sudo sed -i 's;%_dbpath\t.*;%_dbpath\t\t%{_var}/lib/rpm;g' /usr/lib/rpm/macros
```
```
# on debian/ubuntu for some reason yum tries to install packages for
# multiple archs, so it is necessary to update the yum.conf section in
# default.cfg to prevent that. If you switch your default.cfg you may
# have to do this again.
sudo sed -i '/\[main\]/a multilib_policy=best' /etc/mock/default.cfg
```

Whichever distro you are using check that your user is a member of the group mock:

    $ groups

If your user is not a member of the group mock then add them:

    $ usermod -aG mock <your_username>

On Oracle Linux edit your /etc/mock/site-defaults.cfg and add:

    config_opts['use_nspawn'] = False
    
#### Installing `smith` ####

Installing can be done via the Makefile:

    sudo make install


## Using `smith` ##

To use smith, simply create a smith.yaml defining your container and run
`smith`. If you want to overlay additional files or symlinks, simply place them
into a directory called `rootfs` beside smith.yaml.

If you are building the same container multiple times without editing the
package line, the `-f` parameter will rebuild the container without
reinstalling the package.

## Building Microcontainers ##

To build a "hello world" container with `smith`:
1. Create a new directory and cd to it

```
mkdir cat
cd cat
```

2. Create a `smith.yaml` file with the following contents:
```
package: coreutils
paths:
- /usr/bin/cat
cmd:
- /usr/bin/cat
- /read/data
```

3. Create the rootfs directory.  Smith will put the contents of the `./rootfs` directory into the root directory of the image.

`mkdir rootfs`

4. Create the `read` directory under rootfs

`mkdir rootfs/read`

5.  Create the file `data` under `rootfs/read` with the following content:

`Hello World!`

- invoke smith with no parameters:

`smith`

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

For more detailed instructions on building containers, check out:
- [Smith Lab](https://github.com/crush-157/smith-lab)
- [How To Build a Tiny Httpd Container](https://hackernoon.com/how-to-build-a-tiny-httpd-container-ae622c37db39)

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

## Getting in touch ##

The best way to get in touch is Slack.

Click [here](https://join.slack.com/t/oraclecontainertools/shared_invite/enQtMzIwNzg3NDIzMzE5LTIwMjZlODllMWRmNjMwZGM1NGNjMThlZjg3ZmU3NDY1ZWU5ZGJmZWFkOTBjNzk0ODIxNzQ2ODUyNThiNmE0MmI) to join the the [Oracle Container Tools workspace](https://oraclecontainertools.slack.com).

Then join the [Smith channel](https://oraclecontainertools.slack.com/messages/C8BKS9HT5).

## License ##

Copyright (c) 2017, Oracle and/or its affiliates. All rights reserved.

Smith is dual licensed under the Universal Permissive License 1.0 and the
Apache License 2.0.

See [LICENSE](LICENSE.txt) for more details.
