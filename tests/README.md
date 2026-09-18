# Verification

## Automated checks

| Check | What it exercises |
| --- | --- |
| Go unit tests | Prompt/hint precedence, response bytes/limits, cache identity and generations, absolute expiry, retry invalidation, IPC bounds/permissions, parent lifetime, trace redaction |
| Go race detector | Concurrent core/controller/cache/IPC operations |
| Service/device tests | Fake-backend PIN rejection/invalidation, re-verification, device binding, multiple-device confirmation, cancellation during verification, and persistent preference files |
| golangci-lint | Standard Go correctness linters plus gofmt, including integration-tagged code |
| `checks.<system>.integration` | Installed GTK executable on X11: input, empty response, cancellation/window-close, confirmation, signals, parent death, simultaneous windows, cache hits, expiry, key-file changes, forgetting, daemon loss, broken trace pipe |
| OpenSSH cases in integration | Real `ssh-add` wrong-answer retry, successful load, removal/re-add using cache, and cancellation, with an isolated agent and synthetic encrypted key |
| Agent PIN cases in integration | Real `ssh-agent` PIN prompts for software-backed FIDO test keys, repeated signature verification, fingerprint forgetting, and cache reuse through a genuine SSH agent-forwarding channel |
| Worker IPC case | Actual GTK dropdown with saved preselection, explicit confirmation, PIN entry, verifying state, and cleared input on retry |
| `checks.<system>.wayland` | Installed executable on a real headless Weston compositor; notification and input cancellation |
| Passive touch tests | HID descriptor/report parsing, per-channel isolation, bounds, stale expiry, popup aggregation/cleanup, and non-activating X11 windows |
| `checks.<system>.nixos` | Graphical-session startup before any requests, configured TTL, restart clearing, shutdown, socket ownership/mode, and kernel UHID touch input with no output/feature reports |

The GTK suite runs under a private D-Bus session, Xvfb, and Openbox. It does not
use the user's SSH agent or configuration. Its Go parent harness models
independent OpenSSH callers; it launches the normal binary without injecting
answers into the application. Answers are entered through xdotool.

`scripts/integration.sh` builds `tests/sk-provider/provider.c` into a temporary
shared library using a C compiler, pkg-config, and OpenSSL. This software FIDO
fixture requires the synthetic PIN `integration-pin`; it never opens a physical
token. The real agent loads it using an explicit test-only provider allowlist.
`tests/forwarded-agent.py` runs a local Paramiko server for the forwarding case.
Neither fixture is installed in the application package. The Nix shell/check
provide all these test dependencies. `SK_TEST_PROVIDER` may select an already
built copy of this fixture.
The test services use explicit `--pin-verification off` for this provider, which
has no physical CTAP device. Required verification uses a pure-Go fake backend in
service tests; it is not injectable into the production daemon.

The NixOS test additionally creates a disposable USB-HID-shaped FIDO interface
using `/dev/uhid`. It feeds `UPNEEDED` and completion reports through the actual
Linux hidraw driver and checks the passive monitor's metadata events. The fixture
fails if monitoring sends output or feature requests. This runs only in the VM;
it never opens the user's physical token. Parser fuzz targets are in
`internal/touch/report_test.go`.

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
golangci-lint run
bash scripts/integration.sh
go test -race ./internal/app ./internal/askpass ./internal/cache \
  ./internal/cacheipc ./internal/lifecycle ./internal/trace ./internal/service ./internal/preferences ./internal/touch
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
8. For the agent path, load the disposable FIDO key into an isolated agent and
   configure that agent's `SSH_ASKPASS` before startup. Connect using the agent,
   then request a signature through a forwarded agent on the test server. The
   first agent PIN prompt should offer remembering; subsequent operations for
   the same fingerprint should reuse it. Forget the fingerprint and verify that
    input is requested again. Hardware touch is still owned by the token/provider.
9. Enable `touchNotifications` (manual service: `--touch-monitor`) and test a
   browser WebAuthn request as well as SSH/forwarded signing. The popup should
   identify only the device, close on operation completion/error, and remain
   hidden after dismissal until the pending episode ends. Unplug/replug should
   remove/recreate monitoring automatically. The service must already be running
   in the graphical session before any SSH/askpass request.

The software-key integration tests cover wrong-answer retries without consuming
physical authenticator PIN attempts. In required mode, only an explicit device
rejection invalidates a candidate; transport failures are not wrong PINs. In
unverified compatibility mode, forget a rejected candidate before the next attempt.

For required-mode hardware acceptance, additionally check one-device automatic
selection, multiple-device confirmation/preselection, correct PIN verification,
cache-hit re-verification, hotplug/replacement, and reported retries. Use only
correct PINs on the user's normal device. Wrong-PIN and blocked-device cases are
simulated by the fake backend. The implementation uses libfido2 1.17's fresh
PIN/UV-token API, with no credential creation, deletion, or assertions.

## Verification record

On 2026-09-18, the x86_64-linux Nix build and all flake checks passed using the
committed nixpkgs lock, Go 1.26.7, GTK 4.22.4, and OpenSSH 10.5p1. Both X11 and
native Wayland were exercised. The NixOS VM check ran with KVM.

For version 0.3.0 on 2026-09-18, native x86-64 and ARM GitHub CI passed, including
the x86-64 NixOS/UHID check. After deployment, the user confirmed that the passive
touch popup appears when their physical YubiKey awaits touch and closes after
touching it. This is a user-reported hardware acceptance result, not an automated
test of every authenticator or transport. No deliberately incorrect PIN was used
for this passive-monitor acceptance test.

Public CI now schedules native x86-64 and ARM builds and GUI tests on GitHub
Actions; the x86-64 job also runs the NixOS VM. The Actions run status is the
source of truth for verification of a particular pushed commit. Release tooling
has separate checksum/metadata-gate tests under `tests/release/`.
