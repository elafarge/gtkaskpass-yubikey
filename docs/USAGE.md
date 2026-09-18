# Usage and troubleshooting

## Graphical session and agent

The service creates `$XDG_RUNTIME_DIR/ssh-askpass-fido/cache.sock` and runs for
the graphical session. It needs access to the display and FIDO hidraw device;
it does not run as root. On a desktop that does not activate
`graphical-session.target`, import the relevant display variables into the user
manager and start `ssh-askpass-fido.service` explicitly.

An SSH agent keeps the environment it inherited when started. Set `SSH_ASKPASS`
to the installed `ssh-askpass-fido` executable and provide the local graphical
session environment before starting it. A remote Git process cannot change the
local agent's environment. After an agent restart, check `ssh-add -l` and reload
the appropriate identity: an empty forwarded agent cannot authenticate Git.

## Cache controls

```sh
ssh-askpass-fido-service forget --key ~/.ssh/id_ed25519 --kind passphrase
ssh-askpass-fido-service forget --key ~/.ssh/id_ed25519_sk --kind pin
ssh-askpass-fido-service forget --fingerprint 'SHA256:YOUR_KEY_FINGERPRINT'
ssh-askpass-fido-service forget --all
```

Agent entries use public-key fingerprints (`ssh-add -l`); file-based entries are
separate. TTL is absolute and includes suspend, so reuse never extends it. Cached
PINs remain bound to the selected live device connection. Reconnection may
require fresh input. Required verification invalidates explicitly rejected PINs;
in `pinVerification = "off"` compatibility mode, forget rejected candidates
manually. The app never automatically retries a wrong PIN against hardware.

`SSH_ASKPASS_FIDO_CACHE=off` bypasses lookup/storage for an adapter invocation,
but not required hardware verification. Agent-originated requests require the
setting in the agent's environment. No command displays cached credentials.

## Diagnostics

```sh
systemctl --user status ssh-askpass-fido.service
journalctl --user -u ssh-askpass-fido.service
ssh-askpass-fido-service devices
SSH_ASKPASS_FIDO_TRACE=metadata SSH_ASKPASS_REQUIRE=force ssh user@host
```

Metadata tracing redacts credentials. `SSH_ASKPASS_FIDO_TRACE=secrets` explicitly
prints the final response to adapter stderr; use it deliberately. The service
and UI worker never enable secret tracing. Native GTK warnings/errors go to the
service's stderr (normally its journal); informational renderer logs are quiet.

The `devices` diagnostic lists public device metadata and retry counts without
testing a PIN. The passive monitor's journal reports watched/unavailable HID
interfaces. Enable `trace: true` in the service configuration for device-only
touch-state transitions; raw HID reports are never logged.

Touch notifications depend on FIDO2 USB keepalives. NFC/Bluetooth and legacy U2F
polling are not the same signal. Popups identify only the device, not the requesting
application/server. Completion, error, unplugging, or a stale-observation timeout
can close a popup; closure is not proof of authentication success. Wayland focus
policy is controlled by the compositor.

## Persistent preferences

Public device mappings live in
`~/.config/ssh-askpass-fido/devices.json` (or under `$XDG_CONFIG_HOME`). They contain
no PINs, tokens, or retry counts. A usable serial enables persistent preselection;
model names/AAGUIDs alone do not uniquely identify physical keys. Multiple devices
always require confirmation. Stop the service before removing this file to reset
preferences. Credential buffers remain memory-only, without a universal guarantee
of erasure of all Go/GTK copies or immunity from OS swapping.

## Development

```sh
nix develop
go build -o bin/ ./cmd/...
golangci-lint run
go test ./...
bash scripts/integration.sh
nix flake check -L
```

Use the locked Nix environment for compatible GTK/libfido2 versions. The initial
GTK binding build takes several minutes. See [verification](../tests/README.md)
and [releasing](RELEASING.md) for the complete checks and distribution process.
