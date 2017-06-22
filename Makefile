NAME = smith
REMOTE = github.com/oracle

DIRS := \
	. \

uniq = $(if $1,$(firstword $1) $(call uniq,$(filter-out $(firstword $1),$1)))
gofiles = $(foreach d,$(1),$(wildcard $(d)/*.go))
testdirs = $(call uniq,$(foreach d,$(1),$(dir $(wildcard $(d)/*_test.go))))
fmt = $(addprefix fmt-,$(1))

all: $(NAME)

sha = $(shell git rev-parse --short HEAD || cat SHA | tr -d ' \n')
ifeq ($(VERSION),)
VERSION = $(shell git describe --tags --match 'v*.*.*' | tr -d 'v \n')
realv = $(shell printf $(VERSION) | cut -d'-' -f1)
ifneq ($(VERSION),$(realv))
commits = $(shell printf $(VERSION) | cut -d'-' -f2)
VERSION := $(realv).$(commits).$(sha)
endif
endif
dirty = $(shell git diff --shortstat 2> /dev/null | tail -n1 | tr -d ' \n')
ifneq ($(dirty),)
VERSION := $(VERSION).dev
endif
id = $(shell head -c20 /dev/urandom|od -An -tx1|tr -d ' \n')
# this complicated construction is to make sure that the project can be built
# if it is cloned outside of the gopath. Go's vendor support only works if
# the project is inside the gopath
$(NAME): $(call gofiles,$(DIRS))
	mkdir -p build/src/$(REMOTE)
	rm -f build/src/$(REMOTE)/$(NAME) && ln -s ../../../../ build/src/$(REMOTE)/$(NAME)
	cd build/src/$(REMOTE)/$(NAME) && CGO_ENABLED=0 GOPATH=$(CURDIR)/build \
		GO15VENDOREXPERIMENT=1 go build -a -x -v \
		-ldflags '-X "main.ver=$(VERSION)" -X "main.sha=$(sha)" -B 0x$(id)' \
		-o $(NAME) . || ( rm -rf build && false )
	rm -rf build

.PHONY: $(call testdirs,$(DIRS))
$(call testdirs,$(DIRS)):
	sudo -E $(GOPATH)/bin/godep go test -v $(REMOTE)/$(NAME)/$@

$(call fmt,$(call testdirs,$(DIRS))):
	! gofmt -l -w $(subst fmt-,,$@)*.go | grep ''

.PHONY: fmt
fmt: $(call fmt,$(call testdirs,$(DIRS)))

test: fmt $(call testdirs,$(DIRS))

clean:
	rm -f $(NAME)

install: all
	install -D smith $(DESTDIR)/usr/bin/smith
