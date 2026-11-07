#!/bin/sh
# Regenerates testdata/midnight-spec from the reference Erde compiler's spec suite.
#
# usage: _tools/midnight-spec/update.sh <path to erde-lang/erde checkout>
#
# Requires lua (5.1+) and busted (`luarocks install busted`), w/ the erde
# checkout's dependencies installed.
set -eu

erde_repo=$(cd "$1" && pwd)
tools_dir=$(cd "$(dirname "$0")" && pwd)
testdata_dir=$(cd "$tools_dir/../.." && pwd)/testdata/midnight-spec
capture_dir=$(mktemp -d)
trap 'rm -rf "$capture_dir"' EXIT

cd "$erde_repo"
export LUA_PATH="$erde_repo/?.lua;$erde_repo/?/init.lua;${LUA_PATH:-;}"
MIDNIGHT_SPEC_CAPTURE_DIR="$capture_dir" LUA_TARGET=5.4 busted --lua="$(command -v lua)" \
  --helper="$tools_dir/capture_helper.lua" --tags="5.4,5.4%+,5.3%+,5.2%+,5.1%+" > /dev/null

rm -rf "$testdata_dir"
mkdir -p "$testdata_dir/valid" "$testdata_dir/invalid"
lua "$tools_dir/compile.lua" "$capture_dir" "$testdata_dir"
