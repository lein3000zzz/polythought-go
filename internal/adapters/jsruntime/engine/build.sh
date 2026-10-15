#!/bin/sh
# Builds engine.wasm, the JavaScript engine user code runs in: QuickJS-NG with engine.c on
# top, compiled to WASI by wasi-sdk in Docker. Run it with `make jsruntime-wasm` after
# changing engine.c or the versions below, and commit the result, with QuickJS-NG's
# license, which ships with it.
set -eu

QUICKJS_VERSION=0.17.0
QUICKJS_SHA256=559bc4c420475e55c7ab4510adbc562f55d7524d75e8e89d79ce4bb02f5687d9
WASI_SDK_IMAGE=ghcr.io/webassembly/wasi-sdk:wasi-sdk-34@sha256:52595085a0ffbaf574d8d678a9aa81305f0b3af81c639291f15bc7713e42848e

# The engine's C stack; runner.go lets JavaScript use all but a margin of it.
STACK_SIZE=4194304

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -fsSL -o "$work/quickjs.tar.gz" \
	"https://github.com/quickjs-ng/quickjs/archive/refs/tags/v$QUICKJS_VERSION.tar.gz"

docker run --rm --user "$(id -u):$(id -g)" -v "$work:/work" -v "$here:/engine" -w /work \
	"$WASI_SDK_IMAGE" sh -euc "
	echo '$QUICKJS_SHA256  quickjs.tar.gz' | sha256sum -c - >/dev/null
	tar xzf quickjs.tar.gz
	q=quickjs-$QUICKJS_VERSION
	\$CC --target=wasm32-wasip1 -O2 -mexec-model=reactor \
		-D_GNU_SOURCE -D_WASI_EMULATED_PROCESS_CLOCKS -D_WASI_EMULATED_SIGNAL \
		-I\$q -Wno-unused-parameter \
		-o /engine/engine.wasm \
		/engine/engine.c \$q/quickjs.c \$q/libregexp.c \$q/libunicode.c \$q/dtoa.c \
		-Wl,--stack-first -Wl,-z,stack-size=$STACK_SIZE -Wl,--strip-all \
		-lwasi-emulated-process-clocks -lwasi-emulated-signal
	cp \$q/LICENSE /engine/QUICKJS-LICENSE
"

chmod 644 "$here/engine.wasm" "$here/QUICKJS-LICENSE"
echo "built $here/engine.wasm from QuickJS-NG $QUICKJS_VERSION"
