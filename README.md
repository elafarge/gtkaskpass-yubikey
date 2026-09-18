# gtkaskpass-yubikey

[![CI](https://github.com/elafarge/gtkaskpass-yubikey/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/elafarge/gtkaskpass-yubikey/actions/workflows/ci.yml)

Linux SSH askpass in Go, with GTK4 dialogs, an in-memory credential cache, and
FIDO2 PIN verification. A small askpass adapter delegates requests to a headless
per-user service, which starts independent GTK workers for each dialog.

## NixOS setup

Add the flake input and import its module:

```nix
inputs.gtkaskpass-yubikey.url = "github:elafarge/gtkaskpass-yubikey";
```

```nix
{ inputs, ... }: {
  imports = [ inputs.gtkaskpass-yubikey.nixosModules.default ];
  services.gtkaskpass-yubikey = {
    enable = true;
    cacheTTL = "1h";
    pinVerification = "required";
    touchNotifications = true;
  };
}
```

The module sets `SSH_ASKPASS`, installs all three executables, and starts the
service with `graphical-session.target`. It stays running throughout the desktop
session, watching for device touch requests even before the first SSH command.
There is no socket activation. `cacheTTL = "0"` disables
credential caching while keeping the service and PIN verification available.
The user needs normal access to their FIDO hidraw device (typically desktop
session ACLs); the application does not run as root.

For manual testing or non-NixOS Linux:

```sh
nix build
./result/bin/gtkaskpass-yubikey-cache serve --ttl 1h --touch-monitor
```

In another terminal in the graphical session:

```sh
export SSH_ASKPASS="$PWD/result/bin/gtkaskpass-yubikey"
export SSH_ASKPASS_REQUIRE=prefer
ssh user@host
```

The service is required even without caching. It uses
`$XDG_RUNTIME_DIR/gtkaskpass-yubikey/cache.sock`; no shared `/tmp` socket fallback.
The package includes a systemd user service under `share/systemd/user/`.
The service creates its own private IPC socket; this socket does not launch it.
On a desktop that does not start `graphical-session.target`, arrange session
startup or start the service manually after importing the display environment:

```sh
systemctl --user import-environment DISPLAY WAYLAND_DISPLAY XAUTHORITY
systemctl --user start gtkaskpass-yubikey-cache.service
```

Import only variables present in your session. The desktop must also provide its
session bus and runtime directory. One service per UID owns the display/session
environment it started with; it does not jump between simultaneous desktops based
on incoming SSH requests. Restart it after changing the owning graphical session.

The executables are:

| Command | Role |
| --- | --- |
| `gtkaskpass-yubikey` | OpenSSH protocol adapter; owns stdout and cancellation |
| `gtkaskpass-yubikey-cache` | Headless request service and cache controls |
| `gtkaskpass-yubikey-ui` | Private GTK worker, launched by the service |

Upgrade all three together and restart the service after upgrades. Restarting
clears in-memory credentials. Do not invoke the UI worker directly.

## SSH agents and forwarding

Configure `SSH_ASKPASS` and the graphical session environment **in the agent's
environment before starting it**. Setting them only in a local SSH command or a
remote Git process does not reconfigure an existing agent.

Agent PIN prompts are keyed by the full public-key `SHA256:` fingerprint.
Local SSH and forwarded Git operations for that key can reuse the same cached
PIN. No custom SSH patch or `IdentityAgent none` workaround is needed. Your agent
must contain the appropriate key (`ssh-add -l` lists public fingerprints); an
agent restart empties its identity list independently of our PIN cache.

## PIN verification and device choice

In the default **required** mode, the service verifies both newly entered PINs
and cached PINs against the selected FIDO2 device before returning them to SSH.

- **One accessible FIDO2 device with a configured PIN:** automatically selected;
  its identity appears above the PIN field.
- **Multiple devices:** choose one in the dropdown and confirm it. A saved
  preference preselects a unique matching device, but never bypasses confirmation.
- **No eligible device:** a connect-device view refreshes discovery. Devices
  without a configured PIN or inaccessible devices are not eligible.
- **Incorrect PIN:** its cached candidate is invalidated, the answer is not sent
  to SSH, and the dialog asks again. Remaining PIN retries are shown if available.
- **Blocked, busy, disconnected, or unsupported device:** show an error rather
  than silently returning an unverified PIN. No retry is automatic.

The service obtains and immediately discards a fresh PIN-authenticated token via
libfido2. It does not change/reset PINs, create credentials, sign SSH data, or
consume a hardware touch just to observe it. Verification failures consume real
authenticator retry budget; do not deliberately test wrong PINs on your real key.

**PIN accepted is not SSH authentication success.** It proves only that the
selected device accepted that PIN. OpenSSH still selects a device for signing,
validates the signature, and enforces touch. Askpass cannot tell OpenSSH which
device you selected or prove that it contains the requested key.

For a provider/device that cannot support this verification flow, explicitly set
`pinVerification = "off"` (manual service: `--pin-verification off`). This is
unverified compatibility mode: answers are candidates, and a rejected cached PIN
must be forgotten manually. The required mode never falls back to it implicitly.

## Cache and device preferences

Credentials stay in service-owned memory. Default TTL is **one hour from storing
a manual answer**, does not slide on use, and includes suspend. Cached PINs are
bound to the selected device connection: a different or reconnected device is
not automatically given the previous device's PIN.

File-based passphrases/PINs use separate entries per canonical file and metadata
version. Fingerprint-based agent PINs use a separate namespace. Switching between
direct and agent signing may require entering the PIN once in each mode.
Unknown prompts, account passwords, confirmations, empty responses, ambiguous
filenames, and touch notifications are not cached.

Forget credentials with:

```sh
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519 --kind passphrase
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519_sk --kind pin
gtkaskpass-yubikey-cache forget --fingerprint 'SHA256:YOUR_KEY_FINGERPRINT'
gtkaskpass-yubikey-cache forget --all
```

`GTKASKPASS_CACHE=off` bypasses cache lookup/storage for one adapter invocation,
but retains required PIN verification. For agent-originated requests, set it in
the agent environment. Forgetting through the control command takes effect
without restarting the agent.

Device-selection preferences persist in:

```text
${XDG_CONFIG_HOME:-~/.config}/gtkaskpass-yubikey/devices.json
```

The private, atomically written file contains fingerprint-to-device metadata
only—never PINs, PIN hashes, tokens, or retry counts. A successfully delivered,
verified submission records the preference independently of PIN retention. Device serials, where available,
identify a stable preference. A model name, AAGUID, or `/dev/hidrawN` alone does
not uniquely identify a device across reboots. Missing/ambiguous matches require
fresh choice. If identical devices are indistinguishable, disconnect the unwanted
one. Stop the service and remove the file to reset saved preferences.

Memory-only does not promise universal zeroization of Go/GTK copies or unswappable
memory. Owned buffers are cleared on replacement/expiry; the daemon disables core
dumps. Logout clears the cache only if it stops the user service; a lingering
user manager can retain it until expiry.

## Touch notifications

By default, the session service **passively monitors all accessible USB FIDO HID
interfaces**, including devices without a PIN. A FIDO2 `UPNEEDED` keepalive opens
a device-labelled **Touch your security key** popup. This works independently of
SSH, including forwarded Git and browser WebAuthn operations.

The monitor opens devices **read-only** and retrieves cached kernel descriptors.
It sends no FIDO commands, tests no PIN, and never starts an extra touch operation.
Linux delivers copies of incoming HID reports to each open reader; observing them
does not consume OpenSSH's responses. Raw reports are discarded, never logged.

The popup closes when the matching HID-channel transaction completes/errors, the
device disconnects, or three seconds pass without a relevant keepalive. Closing
means the request ended or observation expired, **not authentication success**.
There is one popup per device while any observed channel needs presence. Dismiss
hides it for that pending episode; repeated keepalives do not reopen it, but a
later operation can. Very short requests do not flash a window.

The monitor cannot identify the originating application, SSH key, or server, so
popups show only the device. They do not request activation; X11 also receives
the no-focus-on-map hint. Wayland mapping/focus policy remains compositor-owned.
Passive windows have app ID `io.github.gtkaskpass_yubikey.touch` for compositor
rules, separate from interactive PIN dialogs.

Recognized SSH touch notifications are suppressed when the monitor covers the
connected FIDO interfaces and has a graphical session. Their adapter processes
still follow OpenSSH's SIGTERM lifecycle. When monitoring is unavailable, the
ordinary SSH notification path remains enabled. Other informational askpass
notifications are unaffected.

Hotplug and device ACL changes are picked up within about a second. This monitor
interprets standard FIDO2 USB keepalives; legacy U2F polling, NFC, and Bluetooth
are not equivalent signals. Set `touchNotifications = false` (or omit
`--touch-monitor` for a manual service) to use only OpenSSH-driven notifications.

## Troubleshooting

```sh
GTKASKPASS_TRACE=metadata SSH_ASKPASS_REQUIRE=force ssh user@host
systemctl --user status gtkaskpass-yubikey-cache.service
journalctl --user -u gtkaskpass-yubikey-cache.service
gtkaskpass-yubikey-cache devices
```

Metadata tracing never includes credentials. `GTKASKPASS_TRACE=secrets` explicitly
prints the final response to adapter stderr; it must be enabled deliberately.
The service and UI worker stay metadata-only. Worker/native diagnostics go to
the service stderr (normally its journal); informational GTK/Vulkan logs are
suppressed, while warnings/errors remain. Stdout stays the exact SSH response.
The `devices` diagnostic enumerates accessible PIN-configured FIDO2 devices and
their public connection metadata/retry counts; it never asks for or tests a PIN.
The service journal reports how many FIDO HID interfaces the passive monitor
watches and any unavailable interfaces. `serve --touch-monitor --trace` adds
device-only `touch-state` transitions, never HID packet contents.

A fresh desktop login may be needed for changed session variables; existing
agents retain their old environment. Do not run two services on the same socket.
An unavailable service is an error, not silent verification bypass.

## Development and verification

```sh
nix develop
go build -o bin/ ./cmd/...
golangci-lint run
go test ./...
bash scripts/integration.sh
nix flake check -L
```

Use the pinned Nix environment: Go 1.24+, GTK4 compatible with gotk4 v0.4.1, and
libfido2 1.17+ with the public PIN/UV-token API are required. Cold GTK compilation
takes several minutes. FIDO calls are isolated behind a backend interface;
controller tests simulate wrong PINs without using physical tokens.

Checks include unit/race tests, real adapter/service/GTK IPC, a device chooser and
retry UI test, real OpenSSH agent/forwarding tests with software FIDO fixtures,
Wayland, and a NixOS service VM. The software provider tests run in explicit
unverified compatibility mode and do not claim physical PIN verification.
See [tests/README.md](tests/README.md) for details and hardware acceptance.

CI uses standard public x86-64 and ARM runners, with the KVM VM check on x86-64.
Version tags matching the Nix package version publish checked runtime closures
and checksums through GitHub Releases. See [docs/RELEASING.md](docs/RELEASING.md).

## License

Copyright 2026 Étienne Lafarge. Apache License, Version 2.0: [LICENSE](LICENSE).
Architecture and protocol details: [DESIGN.md](DESIGN.md).
