# gtkaskpass-yubikey

[![CI](https://github.com/elafarge/gtkaskpass-yubikey/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/elafarge/gtkaskpass-yubikey/actions/workflows/ci.yml)

A Linux SSH askpass helper written in Go with GTK4. It provides passphrase/PIN
entry, SSH confirmation dialogs, security-key touch notifications, and a per-user
in-memory credential cache. Works on Wayland and X11.

## Build and try it

```sh
nix build github:elafarge/gtkaskpass-yubikey
export SSH_ASKPASS="$PWD/result/bin/gtkaskpass-yubikey"
export SSH_ASKPASS_REQUIRE=prefer
```

For caching, run this in another terminal, or enable the systemd user service
through the NixOS module below:

```sh
./result/bin/gtkaskpass-yubikey-cache serve --ttl 1h
```

The daemon needs your session's `XDG_RUNTIME_DIR`. It stores credentials only in
process memory and uses `$XDG_RUNTIME_DIR/gtkaskpass-yubikey/cache.sock`.
Stopping or restarting the daemon clears its cache.

To preview a dialog directly:

```sh
./result/bin/gtkaskpass-yubikey "Enter SSH passphrase:"
```

Like every askpass helper, a successful input dialog prints its answer to stdout.
Normally OpenSSH receives that output through a private pipe.

## NixOS

Add this repository as a flake input:

```nix
inputs.gtkaskpass-yubikey.url = "github:elafarge/gtkaskpass-yubikey";
```

Then import its module (with `inputs` supplied through your NixOS `specialArgs`):

```nix
{ inputs, ... }: {
  imports = [ inputs.gtkaskpass-yubikey.nixosModules.default ];

  services.gtkaskpass-yubikey = {
    enable = true;
    cacheTTL = "1h"; # default; e.g. "15m", "2h", or "0" to disable caching
  };
}
```

This configures `programs.ssh.askPassword`, installs the commands, and enables a
systemd **user socket**. The daemon starts on the first cache request. No separate
daemon startup command is needed. The module also exposes a `package` override.

Choose whether to prefer graphical input in your desktop/session configuration:

```sh
export SSH_ASKPASS_REQUIRE=prefer
```

Use `force` when you always want graphical input, including from a terminal.
An already-running SSH agent needs its own `SSH_ASKPASS` and graphical-session
environment; changing an SSH client's environment does not update the agent.

The flake exports packages, an app, development shells, and checks for
`x86_64-linux` and `aarch64-linux`. The standalone recipe at `nix/package.nix`
also supports `pkgs.callPackage` with compatible native dependencies.

For other systemd-based Linux installations, install the units in `packaging/`
(replace `@bindir@` with the installation's absolute binary directory) and run:

```sh
systemctl --user enable --now gtkaskpass-yubikey-cache.socket
```

The Nix package includes ready-to-use units under `share/systemd/user/`.

## How the key flow works

OpenSSH controls the authentication sequence:

1. It requests a local key-file passphrase, if necessary.
2. It requests a FIDO PIN, if necessary; this is distinct from the file passphrase.
3. It launches a `SSH_ASKPASS_PROMPT=none` notification while waiting for presence.
4. It sends the notification SIGTERM when the signing attempt finishes.

Depending on OpenSSH's signing path, a brief touch notification can appear before
the PIN request. These are separate helper invocations; askpass receives no PIN
validation result. Notification termination also occurs on failure, so the UI
does not interpret it as proof of successful authentication.

**For touch notifications, OpenSSH normally uses a terminal when stderr is a
TTY—even with `SSH_ASKPASS_REQUIRE=force`.** Graphical launchers with non-terminal
stderr naturally use askpass. To test the graphical flow from a terminal:

```sh
SSH_ASKPASS_REQUIRE=force ssh user@host 2>ssh-diagnostics.log
```

PIN caching does not bypass hardware touch. The touch dialog's Dismiss button
hides only the notification; SSH continues waiting, and the hidden helper exits
when OpenSSH ends the attempt. To cancel authentication, cancel SSH itself.

Native FIDO SSH keys (`ed25519-sk`, `ecdsa-sk`) are the supported hardware path.
Generic secret prompts also work, but PIV/PKCS#11 and GPG agents have different
touch and PIN lifecycles.

## Cache behavior

- Default lifetime is **one hour from storing a manually submitted answer**.
  Reads do not extend it; suspended time counts toward expiry.
- Passphrases and FIDO PINs have separate entries per canonical key-file path.
  Replacing or modifying the file invalidates its entry.
- A cache hit returns the answer immediately, without opening a dialog.
- On an eligible miss, the input window offers a checked **Remember for …** box.
- Unknown/ambiguous prompts, account passwords, confirmations, empty answers,
  and touch notifications are not cached. `ssh-add -c` prompts are deliberately
  uncached because their annotation is ambiguous with a filename. Paths at
  OpenSSH's known truncation limits are also uncached.
- One YubiKey containing multiple SSH keys has one PIN entry per SSH key, not a
  device-wide entry. The key file must be a regular file owned by the current user.
- An unavailable daemon falls back to ordinary input. Trace metadata explains
  ineligible prompts and cache decisions.

Forget one entry or all entries:

```sh
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519 --kind passphrase
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519_sk --kind pin
gtkaskpass-yubikey-cache forget --all
```

Use the canonical path when forgetting an entry whose symlink has been removed.
Forgetting is a no-op if the cache is empty or its daemon is stopped.

Bypass lookup **and storage** for one command:

```sh
GTKASKPASS_CACHE=off SSH_ASKPASS_REQUIRE=force ssh user@host
```

OpenSSH never tells askpass whether an answer was accepted. The daemon caches
submitted **candidates**. If the same caller requests the same key again, the
helper treats that as a retry and prompts instead of replaying the answer. A
long-lived caller may therefore prompt more often. If a caller exits after a
rejected PIN without retrying, explicitly forget that entry before trying again;
the helper cannot infer that rejection from caller exit or a touch notification.

The daemon is shared by processes of the same UID. Entries use owned byte buffers
that are cleared on expiry/replacement; there is no persistent credential file.
Go/GTK copies and OS swap are outside a guarantee of completely erasable or
unswappable memory. Logout clears the cache when it stops the user service;
a lingering user manager can retain it until expiry.

For software keys, the normal `ssh-agent` can independently retain decrypted
keys with a lifetime. This helper neither changes those settings nor loads keys
into your agent automatically.

## Troubleshooting

```sh
GTKASKPASS_TRACE=metadata SSH_ASKPASS_REQUIRE=force ssh user@host
```

Trace records go to **stderr**, with prompts, mode selection, cache decisions,
window events, signals, output lengths, and exit status. Credential values are
redacted. To include the exact response (quoted/escaped, including its newline):

```sh
GTKASKPASS_TRACE=secrets SSH_ASKPASS_REQUIRE=force ssh user@host
```

`secrets` explicitly prints credentials to stderr and any log receiving it.
Tracing is off by default and never changes askpass stdout.

GTK/GDK informational messages (including verbose Vulkan initialization) are
suppressed by default; warnings and errors remain on stderr. Set
`G_MESSAGES_DEBUG=all` explicitly when troubleshooting native GTK rendering.
This is independent of `GTKASKPASS_TRACE`, whose metadata mode stays useful
without the renderer log flood.

Daemon tracing is metadata-only:

```sh
gtkaskpass-yubikey-cache serve --ttl 1h --trace
```

For an enabled user service, inspect status and normal diagnostics with:

```sh
systemctl --user status gtkaskpass-yubikey-cache.socket gtkaskpass-yubikey-cache.service
journalctl --user -u gtkaskpass-yubikey-cache.service
```

Run only one daemon on a socket. A standalone daemon killed with SIGKILL may
leave its socket behind; after confirming that no daemon is running, remove that
stale socket before restarting. Systemd socket activation manages the listener
independently of daemon restarts.

## Development and tests

```sh
nix develop
go build -o bin/ ./cmd/...
golangci-lint run
go test ./...
go vet ./...
bash scripts/integration.sh
```

The initial gotk4 build takes several minutes. Outside Nix, use Go 1.24 or newer,
a C compiler, pkg-config, and GTK4/GLib/GObject Introspection development packages
compatible with gotk4 v0.4.1 (the pinned environment uses GTK 4.22.4).

The complete reproducible check suite is:

```sh
nix flake check -L
```

It runs golangci-lint, Go unit/race checks, real GTK dialogs under Xvfb, real `ssh-add`
integration with disposable keys/an agent, a headless Weston Wayland smoke test,
and a NixOS VM test of the socket-activated user service. The VM check needs a
builder with KVM support. GUI tests drive the real executable using external
keyboard/window automation; production binaries contain no test secret injector.

See [tests/README.md](tests/README.md) for test layers, individual commands, and
physical-token acceptance testing. See [DESIGN.md](DESIGN.md) for the protocol and
architecture decisions.

## CI and releases

GitHub Actions checks pushes and pull requests on native x86-64 and ARM Linux
runners. It includes golangci-lint, the real GUI/OpenSSH suites, and the x86-64
NixOS VM check. `AGENTS.md` records contributor and coding-agent conventions.

Pushing a stable `vMAJOR.MINOR.PATCH` tag matching the Nix package version runs
the release pipeline. After all checks pass, it publishes native Nix runtime
closures, metadata, and SHA-256 checksums to
[GitHub Releases](https://github.com/elafarge/gtkaskpass-yubikey/releases).

This uses free standard public Actions runners and release downloads, without
Actions artifact storage. GitHub Packages is free for public packages but has no
native Nix/Go registry, so release assets are the distribution channel here.
See [docs/RELEASING.md](docs/RELEASING.md) for the billing references, release
procedure, permissions, and prebuilt-package installation instructions.

## License

Copyright 2026 Étienne Lafarge. Apache License, Version 2.0. See [LICENSE](LICENSE).
