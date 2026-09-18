# Verification

## Automated checks

| Check | What it exercises |
| --- | --- |
| Go unit tests | Prompt/hint precedence, response bytes/limits, cache identity and generations, absolute expiry, retry invalidation, IPC bounds/permissions, parent lifetime, trace redaction |
| Go race detector | Concurrent core/controller/cache/IPC operations |
| `checks.<system>.integration` | Installed GTK executable on X11: input, empty response, cancellation/window-close, confirmation, signals, parent death, simultaneous windows, cache hits, expiry, key-file changes, forgetting, daemon loss, broken trace pipe |
| OpenSSH cases in integration | Real `ssh-add` wrong-answer retry, successful load, removal/re-add using cache, and cancellation, with an isolated agent and synthetic encrypted key |
| `checks.<system>.wayland` | Installed executable on a real headless Weston compositor; notification and input cancellation |
| `checks.<system>.nixos` | Real systemd user socket activation, configured TTL, expiry, restart clearing, concurrent clients, and socket ownership/mode in a NixOS VM |

The GTK suite runs under a private D-Bus session, Xvfb, and Openbox. It does not
use the user's SSH agent or configuration. Its Go parent harness models
independent OpenSSH callers; it launches the normal binary without injecting
answers into the application. Answers are entered through xdotool.

Run all checks with `nix flake check -L`. Run individual installed-package checks:

```sh
nix build .#checks.x86_64-linux.integration -L
nix build .#checks.x86_64-linux.wayland -L
nix build .#checks.x86_64-linux.nixos -L
```

For fast iteration:

```sh
nix develop
go build -o bin/ ./cmd/...
bash scripts/integration.sh
go test -race ./internal/app ./internal/askpass ./internal/cache \
  ./internal/cacheipc ./internal/lifecycle ./internal/trace
```

`ASKPASS_BIN` and `CACHE_BIN` can select alternate built executables for the GUI
suite. The integration build tag requires its dependencies; missing tools are an
error. Ordinary `go test ./...` runs the display-independent tests.

## Physical FIDO acceptance

Automated tests establish the askpass interface and notification lifecycle, not
the behavior of a particular physical authenticator. With a disposable test
identity authorized on a test SSH server:

1. Use a native FIDO identity with user verification required, and optionally
   an encrypted local key-handle file. Keep a touch-only identity for comparison.
2. Configure `SSH_ASKPASS` to the built helper and run its cache daemon or enable
   the NixOS module.
3. Force the direct-client path and non-terminal stderr:

   ```sh
   GTKASKPASS_TRACE=metadata SSH_ASKPASS_REQUIRE=force \
     ssh -o IdentityAgent=none -o IdentitiesOnly=yes \
     -i /path/to/disposable_fido_key user@test-server 2>ssh-test.log
   ```

4. Check that requested passphrase/PIN dialogs return their responses, a touch
   notification appears when OpenSSH emits it, and touching the key completes
   signing and dismisses the notification. An initial brief notification before
   the PIN request is valid OpenSSH behavior.
5. Reconnect within the TTL: eligible secrets should come from the cache while
   hardware touch remains required. Forget each kind of entry and confirm that
   its input dialog returns. Repeat with a short TTL.
6. Dismiss a touch window and confirm that SSH continues waiting. Cancel SSH
   and verify that no notification process remains.
7. Repeat with the touch-only identity, without the device, and with concurrent
   independent SSH connections. Operation failure must close the notification
   without claiming successful authentication.

The software-key integration tests cover wrong-answer retries without consuming
physical authenticator PIN attempts. A failed PIN operation that does not retry
cannot be inferred by askpass; forget that candidate before the next attempt.

## Verification record

On 2026-09-18, the x86_64-linux Nix build and all flake checks passed using the
committed nixpkgs lock, Go 1.26.7, GTK 4.22.4, and OpenSSH 10.5p1. Both X11 and
native Wayland were exercised. The NixOS VM check ran with KVM.

The aarch64-linux outputs are provided but have not been built on an ARM builder.
Physical YubiKey acceptance has not been performed in this environment.
