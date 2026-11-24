#!/bin/sh
# Builds the vex command into bin/.
set -eu

cd "$(dirname "$0")"

base=0.1.0-dev
rev=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	rev="$rev-dirty"
fi

mkdir -p bin
go build -trimpath -ldflags "-s -w -X main.version=$base+$rev" -o bin/vex ./cmd/vex
echo "Built bin/vex ($(bin/vex version))"
