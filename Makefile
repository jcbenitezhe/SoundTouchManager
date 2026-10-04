# STM Makefile
# Targets:
#   make build              local binary for the current platform
#   make build-arm          armv7l (SoundTouch 10/20/30, real target)
#   make build-arm64        arm64 (reserve)
#   make build-all          all architectures
#   make winformat-embed    cross-compile the FAT32 helper and drop
#                           it into sticksetup/embedded/ so go:embed
#                           picks it up. Cross-compiles from any host.
#   make agent-embed        same idea for the ARM stick agent that
#                           the desktop app embeds via go:embed.
#   make engine-embed       pull the latest release's go-librespot Spotify
#                           engine into the embed slot so a LOCAL desktop
#                           build can push it to the box after an OTA. A clean
#                           checkout ships a 0-byte stub, so without this a
#                           fleet roll from a dev build leaves boxes without
#                           Spotify. Restore the stub before committing.
#   make wails-dev          run the desktop app in dev mode with the
#                           embedded helpers freshly built. The one
#                           command you run for everyday work.
#   make wails-build        production build of the desktop app
#                           with embedded helpers and version stamp.
#   make test               go test ./...
#   make vet                go vet ./...
#   make tidy               go mod tidy
#   make clean              wipe build outputs (keeps stubs)

BINARY      := stmanager
PKG         := ./cmd/agent
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_STAMP ?= $(shell date '+%Y-%m-%d-%H%M')

# Freeze both, once. `?=` creates a RECURSIVELY expanded variable, so
# `$(shell ...)` re-runs at every single reference: measured 2026-09-08, two
# references a millisecond apart returned different values. That matters because
# `wails-build` stamps the embedded ARM agent in one recipe line and the app
# itself in the next, minutes later. Cross a minute boundary and the app is
# stamped newer than the agent it just installed, so the version comparison
# reads a freshly updated speaker as out of date and the OTA banner never
# clears. (git describe re-runs too, and agent-embed writes into the tracked
# embed slot, so a clean checkout could even go from "v0.9.76" to
# "v0.9.76-dirty" between the two lines.) These two lines keep the ?= override
# semantics for the environment and the command line, and pin the result.
VERSION     := $(VERSION)
BUILD_STAMP := $(BUILD_STAMP)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.buildStamp=$(BUILD_STAMP)
# Do not try to keep symbols in the desktop app by removing -s -w here: Wails
# appends its own "-w -s" after ours whenever it builds in production mode
# (wails v2.13.0 pkg/commands/build/base.go:255), so the flags come back and the
# binary is byte for byte identical. Measured 2026-08-17 while looking for ways
# to reduce antivirus false positives on the Windows build.
APP_LDFLAGS := -s -w -X main.appVersion=$(VERSION) -X main.appBuild=$(BUILD_STAMP)
GO          ?= go

# Wails needs WebKitGTK, and distributions have moved on: Fedora 44 ships only
# webkit2gtk4.1-devel, with no 4.0 package at all. Wails v2 still defaults its
# pkg-config to webkit2gtk-4.0 (+ libsoup-2.4), so a Linux build without this
# tag dies at cgo with "Package webkit2gtk-4.0 was not found". The tag switches
# it to webkit2gtk-4.1 + libsoup-3.0. release.yml passes exactly the same tag
# for linux/amd64; only the local build was missing it. Linux-only, because the
# tag gates linux build files and macOS/Windows have no use for it.
ifeq ($(shell uname -s),Linux)
WAILS_TAGS  ?= webkit2_41
endif
WAILS_TAGFLAG := $(if $(WAILS_TAGS),-tags $(WAILS_TAGS),)

# Some Windows make builds do not pass the inherited environment into recipe
# sub-shells: TMP/TEMP vanish (Go dies with "mkdir C:\WINDOWS\go-buildN:
# Zugriff verweigert") and even USERPROFILE/GOPATH disappear ("module cache
# not found"). Recurring since 2026-07-26. Two-part durable fix: pin Go's
# scratch dir to a repo-local folder that needs no environment at all, and
# force-export the variables Go derives its defaults from. Explicitly
# exported make variables DO reach the sub-shell even when plain inherited
# ones do not. Harmless on Linux/macOS/CI (values pass through unchanged).
export GOTMPDIR := $(CURDIR)/.gotmp
$(shell mkdir -p $(CURDIR)/.gotmp)
export HOME USERPROFILE TMP TEMP APPDATA LOCALAPPDATA GOPATH GOMODCACHE GOCACHE GOFLAGS PATH

# Embed targets — must exist before go:embed in desktop-app/agentbin
# and sticksetup respectively. CI overwrites the empty stubs that
# are checked in; these targets do the same locally.
WINFORMAT_OUT := sticksetup/embedded/winformat.exe
AGENT_EMBED_OUT := desktop-app/agentbin/stmanager-armv7l
ENGINE_EMBED_OUT := desktop-app/agentbin/go-librespot-armv7l

ANDROID_BACKEND_OUT := android/app/src/main/jniLibs/arm64-v8a/libstmbackend.so
ANDROID_BACKEND_V7_OUT := android/app/src/main/jniLibs/armeabi-v7a/libstmbackend.so
IOS_BACKEND_DIR := ios/Backend
XCODEGEN ?= xcodegen

.PHONY: all build build-arm build-arm64 build-all \
        winformat-embed agent-embed engine-embed engine-build winres wails-dev wails-build \
        frontend-build android-backend ios-backend ios-project bridge-dev \
        test vet tidy clean

all: build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

# GOARM=5, not 7, on purpose. Some early SoundTouch units (seen on an
# older ST20 on 2017 firmware) have a CPU/kernel without
# working VFP hardware float. A GOARM=6/7 binary emits VFP instructions
# and SIGILLs at the first stdlib float touch (os.init), crash-looping
# the agent and soft-bricking the box. GOARM=5 is pure software float
# with kernel-helper atomics: no ARMv7-optional instructions, so it runs
# on every SoundTouch CPU revision (a compat superset of GOARM=7). The
# agent does no heavy FP, so the softfloat cost is negligible. Keep this
# in sync with release.yml / build.yml (goarm matrix) and agent-embed.
build-arm:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-armv7l $(PKG)

build-arm64:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-arm64 $(PKG)

build-all: build build-arm build-arm64

# Cross-compiled, no CGO — works from Windows, Linux or macOS host.
# Drops the real binary into the embed slot so the next `go build`
# of the package picks it up; without this the stub stays empty
# and sticksetup.formatVolume errors with "winformat Helper fehlt".
winformat-embed:
	@mkdir -p $(dir $(WINFORMAT_OUT))
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags="-s -w" -o $(WINFORMAT_OUT) ./cmd/winformat
	@echo "embedded $$(stat -c %s $(WINFORMAT_OUT) 2>/dev/null || stat -f %z $(WINFORMAT_OUT)) bytes into $(WINFORMAT_OUT)"

# Cross-compile the stick agent for the real ARMv7l target and
# drop it into desktop-app/agentbin so the desktop app's go:embed
# picks it up. Required for OTA-from-app to actually push a binary.
agent-embed:
	@mkdir -p $(dir $(AGENT_EMBED_OUT))
	GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(AGENT_EMBED_OUT) $(PKG)
	@echo "embedded $$(stat -c %s $(AGENT_EMBED_OUT) 2>/dev/null || stat -f %z $(AGENT_EMBED_OUT)) bytes into $(AGENT_EMBED_OUT)"

# Pull the latest release's go-librespot Spotify engine into the embed slot so a
# LOCAL wails build can re-deliver it to a box after an OTA, exactly like a
# release build does. Only CI fills this slot normally; a clean checkout keeps a
# 0-byte stub, so a fleet roll from a dev build otherwise leaves every box with
# Spotify missing (the agent OTA drops the ~16 MB engine to fit and the dev app
# has nothing to push back). Run this once before `make wails-build` /
# `make wails-dev` when you want a fleet-capable dev build; agent-embed rebuilds
# only the agent, so the fetched engine survives later wails builds.
#
# The filled binary is a tracked stub like the agent one: RESTORE IT with
# `git checkout -- $(ENGINE_EMBED_OUT)` before committing (the release-skill
# triage does this too). Needs the gh CLI and a network connection.
#
# ENGINE_REPO is the repository whose latest release carries the engine;
# override with `make engine-embed ENGINE_REPO=owner/name`. Without a release
# to pull from, `make engine-build` compiles the same engine locally.
ENGINE_REPO ?= jcbenitezhe/SoundTouchManager
engine-embed:
	@command -v gh >/dev/null 2>&1 || { echo "engine-embed needs the gh CLI (https://cli.github.com)"; exit 1; }
	@tag=$$(gh release view --repo $(ENGINE_REPO) --json tagName --jq .tagName 2>/dev/null); \
	if [ -z "$$tag" ]; then echo "engine-embed: could not read the latest release tag (is gh authenticated?)"; exit 1; fi; \
	echo "engine-embed: fetching go-librespot-armv7l from $(ENGINE_REPO) release $$tag"; \
	gh release download "$$tag" --repo $(ENGINE_REPO) -p go-librespot-armv7l -D $(dir $(ENGINE_EMBED_OUT)) --clobber
	@echo "embedded $$(stat -c %s $(ENGINE_EMBED_OUT) 2>/dev/null || stat -f %z $(ENGINE_EMBED_OUT)) bytes into $(ENGINE_EMBED_OUT) -- restore the stub with 'git checkout -- $(ENGINE_EMBED_OUT)' before committing"

# Build the same engine locally from the vendored fork in third_party/, with
# the lean recipe CI uses (.github/scripts/golibrespot-lean-prep.sh, pure Go,
# GOARM=5). The prep strips and patches files, so it runs on a scratch copy and
# third_party/ is never modified. Same stub caveat as engine-embed.
ENGINE_BUILD_DIR ?= $(abspath .engine-build)
engine-build:
	rm -rf $(ENGINE_BUILD_DIR) && mkdir -p $(ENGINE_BUILD_DIR)
	cp -R .github $(ENGINE_BUILD_DIR)/.github
	cp -R third_party/go-librespot $(ENGINE_BUILD_DIR)/go-librespot
	git -C $(ENGINE_BUILD_DIR)/go-librespot init -q
	cd $(ENGINE_BUILD_DIR) && sh .github/scripts/golibrespot-lean-prep.sh
	cd $(ENGINE_BUILD_DIR)/go-librespot && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 \
		$(GO) build -trimpath -buildvcs=false -ldflags "-s -w" -o $(abspath $(ENGINE_EMBED_OUT)) ./cmd/daemon
	rm -rf $(ENGINE_BUILD_DIR)
	@echo "built $$(stat -c %s $(ENGINE_EMBED_OUT) 2>/dev/null || stat -f %z $(ENGINE_EMBED_OUT)) bytes into $(ENGINE_EMBED_OUT) -- restore the stub with 'git checkout -- $(ENGINE_EMBED_OUT)' before committing"

# Run the desktop app in dev mode with embedded helpers freshly
# built so format and OTA features actually work locally. The
# `-reloaddirs ..` flag makes wails dev rebuild the Go backend
# when a file in the root module (discovery, internal, cmd)
# changes — not just the desktop-app dir.
wails-dev: winformat-embed agent-embed
	cd desktop-app && wails dev \
		$(WAILS_TAGFLAG) \
		-ldflags "$(APP_LDFLAGS)" \
		-reloaddirs ".."

# Generate the Windows resource ourselves: icon, manifest AND the version
# block. Wails writes the first two and silently skips the third, which left
# every released Windows binary with no publisher, product name or version at
# all. That is what the first-run warning shows, and it is one of the things an
# antivirus heuristic weighs. The Go linker refuses two resource sections, so
# this replaces the Wails one and the build below passes -nopackage. Windows
# only: on macOS packaging is what builds the .app bundle.
winres:
	cd desktop-app && $(GO) run ./cmd/winresgen \
		-out rsrc_windows_amd64.syso \
		-version "$(VERSION)" \
		-comments "SoundTouch Manager. Unofficial, not affiliated with or endorsed by Bose."

# Production-style local build. Embed slots populated, version
# stamps wired in. Outputs to desktop-app/build/bin/.
wails-build: winformat-embed agent-embed winres
	cd desktop-app && wails build \
		$(WAILS_TAGFLAG) \
		-ldflags "$(APP_LDFLAGS)" \
		-trimpath \
		-clean \
		-nopackage

frontend-build:
	cd desktop-app/frontend && npm run build

# The Android app runs the desktop backend as a child process: the bridge build
# (desktop-app/bridge.go) serves the same frontend to a WebView over loopback.
# It is packaged as a "lib*.so" because the native library directory is the one
# place Android still lets an app execute a file from. Pure Go links for
# android/arm64 only; android/arm needs cgo, so 32-bit phones get a static
# linux/arm build instead (the kernel runs it the same), with the stmandroid tag
# for the Android network setup. agent-embed is a dependency so OTA from the
# phone carries an agent with the same version stamp.
android-backend: agent-embed frontend-build
	@mkdir -p $(dir $(ANDROID_BACKEND_OUT)) $(dir $(ANDROID_BACKEND_V7_OUT))
	cd desktop-app && GOOS=android GOARCH=arm64 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags "$(APP_LDFLAGS)" -o ../$(ANDROID_BACKEND_OUT) .
	cd desktop-app && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
		$(GO) build -tags stmbridge,stmandroid -trimpath -ldflags "$(APP_LDFLAGS)" -o ../$(ANDROID_BACKEND_V7_OUT) .
	@for f in $(ANDROID_BACKEND_OUT) $(ANDROID_BACKEND_V7_OUT); do \
		echo "built $$(stat -c %s $$f 2>/dev/null || stat -f %z $$f) bytes into $$f"; done

# The iOS app cannot start a child process, so the same bridge is built as a
# static library (c-archive) and linked into the app, one per SDK: device and
# Apple Silicon simulator. Needs Xcode for the iOS SDKs. ios-project then
# generates ios/SoundTouchManager.xcodeproj with XcodeGen.
IOS_MIN := 15.0
ios-backend: agent-embed frontend-build
	@xcrun --sdk iphoneos --show-sdk-path >/dev/null || { echo "Xcode with the iOS SDK is required"; exit 1; }
	@mkdir -p $(IOS_BACKEND_DIR)/iphoneos $(IOS_BACKEND_DIR)/iphonesimulator $(IOS_BACKEND_DIR)/include
	cd desktop-app && GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
		CC="$$(xcrun --sdk iphoneos -f clang) -isysroot $$(xcrun --sdk iphoneos --show-sdk-path) -target arm64-apple-ios$(IOS_MIN)" \
		$(GO) build -tags stmbridge -buildmode=c-archive -trimpath -ldflags "$(APP_LDFLAGS)" -o ../$(IOS_BACKEND_DIR)/iphoneos/libstmbackend.a .
	cd desktop-app && GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
		CC="$$(xcrun --sdk iphonesimulator -f clang) -isysroot $$(xcrun --sdk iphonesimulator --show-sdk-path) -target arm64-apple-ios$(IOS_MIN)-simulator" \
		$(GO) build -tags stmbridge -buildmode=c-archive -trimpath -ldflags "$(APP_LDFLAGS)" -o ../$(IOS_BACKEND_DIR)/iphonesimulator/libstmbackend.a .
	mv $(IOS_BACKEND_DIR)/iphoneos/libstmbackend.h $(IOS_BACKEND_DIR)/include/libstmbackend.h
	rm -f $(IOS_BACKEND_DIR)/iphonesimulator/libstmbackend.h

ios-project:
	cd ios && $(XCODEGEN) generate

# Serve the Android UI from this machine for a quick look in a desktop browser.
# Prints the URL (with its access token) on start.
bridge-dev: frontend-build
	cd desktop-app && $(GO) run -tags stmbridge -ldflags "$(APP_LDFLAGS)" .

# Regenerate the supplemental root certificates the agent embeds, from the
# tracked Mozilla bundle. The pinned list lives in the script, so every change
# to what STM trusts is a reviewed edit rather than a silent refresh.
ca-roots:
	python internal/tlsgen/extractroots.py


test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)
	rm -rf desktop-app/build/bin
