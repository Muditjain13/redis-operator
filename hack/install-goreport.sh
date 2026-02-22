#!/usr/bin/env bash
# Install goreportcard-cli and the tools it shells out to.
#
# goreportcard-cli runs its checks through gometalinter, which is archived and
# no longer builds as a Go module (its kingpin dependency moved on). Its tagged
# release vendors the right dependencies, so build it in legacy GOPATH mode.
set -euo pipefail

bin="$(go env GOPATH)/bin"

go install github.com/gojp/goreportcard/cmd/goreportcard-cli@latest
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
go install github.com/gordonklaus/ineffassign@latest
go install github.com/client9/misspell/cmd/misspell@latest

work="$(mktemp -d)"
trap 'chmod -R u+w "${work}" 2>/dev/null; rm -rf "${work}"' EXIT
src="${work}/src/github.com/alecthomas/gometalinter"
git clone -q --depth 1 --branch v3.0.0 https://github.com/alecthomas/gometalinter "${src}"
(cd "${src}" && GOPATH="${work}" GO111MODULE=off GOFLAGS= go build -o "${bin}/gometalinter" .)

echo "Installed into ${bin}:"
ls "${bin}"
