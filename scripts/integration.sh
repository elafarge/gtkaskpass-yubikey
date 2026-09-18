#!/usr/bin/env bash
set -euo pipefail
export ASKPASS_BIN="${ASKPASS_BIN:-$PWD/bin/ssh-askpass-fido}"
export CACHE_BIN="${CACHE_BIN:-$PWD/bin/ssh-askpass-fido-service}"
export SSH_ASKPASS_FIDO_TEST_DISPLAY=1
export GDK_BACKEND=x11
export GSK_RENDERER=cairo
export GTK_A11Y=none
unset WAYLAND_DISPLAY
if [[ -z "${SK_TEST_PROVIDER:-}" ]]; then
  provider_dir=$(mktemp -d)
  trap 'rm -rf "$provider_dir"' EXIT
  openssl_flags=$(pkg-config --cflags --libs openssl)
  read -r -a cflags <<< "$openssl_flags"
  cc -shared -fPIC -Wall -Wextra -Werror tests/sk-provider/provider.c \
    -o "$provider_dir/provider.so" "${cflags[@]}"
  export SK_TEST_PROVIDER="$provider_dir/provider.so"
fi
dbus-run-session --config-file="$PWD/tests/session.conf" -- xvfb-run -a go test -count=1 -timeout=180s -tags=integration -v ./tests/integration
