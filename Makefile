USERNAME ?= wrboyce
PROJECT ?= $(shell basename $(CURDIR))
EXECUTABLE ?= $(PROJECT)
DOCKER_IMAGE ?= $(USERNAME)/$(PROJECT)
BRANCH ?= $(shell git branch --format='%(refname:short)' 2>/dev/null)
COMMIT := $(shell git show -s --format='%h' 2>/dev/null)
VERSION := $(shell git describe --abbrev=0 --tags --dirty 2>/dev/null)
BUILD_PLATFORMS ?= darwin/386 darwin/amd64 linux/386 linux/amd64 linux/arm freebsd/386 freebsd/amd64 openbsd/386 openbsd/amd64 freebsd/arm netbsd/386 netbsd/amd64 netbsd/arm solaris/amd64 linux/arm64
SHASUM ?= sha256sum

all: bin/$(EXECUTABLE)

dep:
	go get -v ./...
	go get github.com/mitchellh/gox

lint-dep: dep
	go get github.com/golang/lint/golint
	go get golang.org/x/tools/cmd/goimports

lint: lint-dep
	gofmt -e -l -s .
	goimports -d .
	golint -set_exit_status ./...
	go tool vet .

test-dep: dep
	go test -i -v ./...

test: test-dep
	go test -v ./...

release: $(addsuffix .tar.gz,$(addprefix build/$(EXECUTABLE)-$(VERSION)_,$(subst /,_,$(BUILD_PLATFORMS))))
release: $(addsuffix .tar.gz.sha256,$(addprefix build/$(EXECUTABLE)-$(VERSION)_,$(subst /,_,$(BUILD_PLATFORMS))))

container:
	docker build --pull --tag $(DOCKER_IMAGE):$(COMMIT) .

container-upload:
	docker push $(DOCKER_IMAGE):$(COMMIT)
	docker tag $(DOCKER_IMAGE):$(COMMIT) $(DOCKER_IMAGE):$(BRANCH)
	docker push $(DOCKER_IMAGE):$(BRANCH)
ifeq ($(BRANCH),master)
	docker tag $(DOCKER_IMAGE):$(COMMIT) $(DOCKER_IMAGE):latest
	docker push $(DOCKER_IMAGE):latest
endif
ifeq ($(VERSION),$(shell git describe --exact-match --tags 2>&1))
	docker tag $(DOCKER_IMAGE):$(COMMIT) $(DOCKER_IMAGE):$(VERSION)
	docker push $(DOCKER_IMAGE):$(VERSION)
endif

upload-dep:
	go get github.com/aktau/github-release

release-upload: lint test upload-dep
ifndef GITHUB_TOKEN
	$(error GITHUB_TOKEN is undefined)
endif
ifneq ($(VERSION),$(shell git describe --exact-match --tags 2>&1))
	$(error refusing to upload dirty release)
endif
	git log --format='* %s' --grep='change-type' --regexp-ignore-case $(shell git describe --tag --abbrev=0 $(VERSION)^)...$(VERSION) | \
		github-release release -u $(USERNAME) -r $(PROJECT) -t $(VERSION) -n $(VERSION) -d - || true
	$(foreach FILE, $(addsuffix .tar.gz,$(addprefix build/$(EXECUTABLE)-$(VERSION)_,$(subst /,_,$(BUILD_PLATFORMS)))), \
		github-release upload -u $(USERNAME) -r $(PROJECT) -t $(VERSION) -n $(notdir $(FILE)) -f $(FILE) && \
		github-release upload -u $(USERNAME) -r $(PROJECT) -t $(VERSION) -n $(notdir $(addsuffix .sha256,$(FILE))) -f $(addsuffix .sha256,$(FILE)) ;)

clean:
	rm -vrf bin/* build/*

# binary
bin/$(EXECUTABLE): dep
	go build -ldflags="-X main.version=$(VERSION)" -o "$@" -v ./$(PACKAGE)
# release binaries
build/%/$(EXECUTABLE): dep
	gox -parallel=1 -osarch=$(subst _,/,$(subst build/,,$(@:/$(EXECUTABLE)=))) -ldflags="-X main.version=$(VERSION)" -output="build/{{.OS}}_{{.Arch}}/$(EXECUTABLE)" ./$(PACKAGE)
# compressed artifacts
build/$(EXECUTABLE)-$(VERSION)_%.tar.gz: build/%/$(EXECUTABLE)
	tar -zcf "$@" -C "$(dir $<)" $(EXECUTABLE) || tar -zcf "$@" -C "$(dir $<)" $(EXECUTABLE).exe
# signed artifacts
%.sha256: %
	cd $(dir $<) && $(SHASUM) $(notdir $<) > $(addsuffix .sha256,$(notdir $<))

.PHONY: dep lint-dep lint test-dep test release upload-dep upload clean
