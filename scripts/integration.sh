#!/usr/bin/env bash
set -euo pipefail
export ASKPASS_BIN="${ASKPASS_BIN:-$PWD/bin/gtkaskpass-yubikey}"
export CACHE_BIN="${CACHE_BIN:-$PWD/bin/gtkaskpass-yubikey-cache}"
export GTKASKPASS_TEST_DISPLAY=1
export GDK_BACKEND=x11
export GSK_RENDERER=cairo
export GTK_A11Y=none
unset WAYLAND_DISPLAY
exec dbus-run-session --config-file="$PWD/tests/session.conf" -- xvfb-run -a go test -count=1 -timeout=180s -tags=integration -v ./tests/integration
