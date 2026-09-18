# Contributor and coding-agent notes

## Project and layout

- Linux-only Go application using GTK4 through gotk4 and CGO.
- Read `DESIGN.md` for protocol/architecture decisions and `README.md` for usage.
- `cmd/gtkaskpass-yubikey`: thin protocol adapter; `cmd/gtkaskpass-yubikey-cache`:
  headless request/verification/cache service; `cmd/gtkaskpass-yubikey-ui`: GTK worker.
- Keep protocol/controller/cache code under `internal/` independent of GTK.
  GTK imports belong in `internal/gtkui` and worker wiring. Native FIDO access
  belongs in `internal/fido`, behind the pure-Go `internal/device` interface.
- `nix/package.nix` is the standalone package recipe; `flake.nix` exposes packages,
  checks, and development shells; `nix/module.nix` configures NixOS user services.
- Project code and documentation are Apache-2.0, copyright Étienne Lafarge.

## Build and check

Use the locked Nix environment: the generated GTK bindings need newer native
libraries than many distribution defaults. A cold gotk4 build takes several minutes.

```sh
nix develop --command go build -o bin/ ./cmd/...
nix develop --command golangci-lint run
nix develop --command go test ./...
nix develop --command bash scripts/integration.sh
nix build
nix flake check -L
```

- Format Go changes with `gofmt`; lint configuration is `.golangci.yml`.
- golangci-lint includes integration-tagged Go code, not only production packages.
  Fix findings rather than introducing blanket exclusions. Explicitly discard an
  error only when it is intentionally best-effort (for example cleanup or tracing).
- Nix package checks run lint, vet, unit tests, and pure-Go race tests.
- Integration tests drive the **real executable** with xdotool under a private
  D-Bus session, Xvfb, and Openbox. Do not add production test-secret injection.
- The Wayland check uses headless Weston. The NixOS VM check needs KVM; GitHub CI
  runs it on x86-64. Report unavailable hardware/builders accurately, never as passes.
- `tests/README.md` describes physical-token acceptance. Do not automate failed PIN
  attempts on a user's real key or use their normal SSH agent/configuration in tests.
- Run relevant checks after changes; avoid repeatedly rebuilding GTK unnecessarily.
  The Nix package's explicit source fileset must include new build/check inputs.

## Askpass invariants

- Prompt text is data, including leading `-`, whitespace, and newlines. Known
  `SSH_ASKPASS_PROMPT` hints take precedence over textual classification.
- Stdout is the SSH response protocol: exact response plus LF, or no bytes on
  cancellation/notification. Diagnostics and trace output go only to stderr.
- Credential responses must not appear in ordinary diagnostics or metadata traces.
  Only the explicit helper `GTKASKPASS_TRACE=secrets` mode includes them. Daemon
  tracing is always metadata-only. Broken stderr must not break authentication.
- OpenSSH owns signing and authentication. The service verifies FIDO PINs before
  returning them in required mode. PIN input and touch notifications are
  separate invocations; `none` notifications finish on SIGTERM, including failures.
  Never infer successful authentication from notification termination.
- Dismissing a notification hides it without ending its process; parent death and
  SIGTERM still close it. Input cancellation returns nonzero without a response.
- Passive device notifications are separate from SSH helpers. `internal/touch`
  reads USB FIDO hidraw input only: no writes, feature reports, PIN probes, or
  assertions. Never log raw HID payloads or interpret popup closure as success.
  Track device/channel state and bound it; unrelated traffic must not dismiss
  an operation. Keep monitoring independent of the verification device lock.
- The service runs with the graphical session, not on socket activation. Passive
  popups use that session's environment; incoming requests must not redirect it.
- GTK must run on the worker's initial locked OS thread, with widget access on
  its main context. Each request has an independent UI worker; only the adapter
  writes the SSH response to stdout.

## Cache invariants

- Keep all retained credentials in memory. No secret files, argv, environment
  transport, daemon log payloads, or command to dump cached values.
- Separate PINs and passphrases per canonical key file and metadata version.
  Agent PINs use a separate canonical SHA256-fingerprint namespace; do not treat
  an agent fingerprint as a filesystem path. Unknown/ambiguous prompts and
  non-key credentials remain uncached.
- TTL defaults to one hour, is absolute, and includes suspend. Hits never refresh it.
- PIN acceptance is not proof of SSH authentication or credential ownership.
  Required mode verifies cached and manual PINs on the selected device. Explicit
  rejection invalidates its generation; do not auto-retry. Preserve same-caller
  retry bypass for file-based requests. Agent fingerprint prompts permit reuse
  across operations from the same agent (OpenSSH asks once per signing operation).
  Unverified compatibility mode requires explicit forgetting after rejection.
  Never infer validation from notification termination. Preserve
  generation-checked stores/invalidations. Concurrent or forgotten
  entries must not be overwritten/resurrected by a stale request.
- Validate Unix socket/directory ownership and modes, check peer UID, bound frame
  and cache sizes, and keep IPC deadlines. Service failure never silently bypasses
  required PIN verification or replays a request after acceptance.
- Persistent device preferences contain public identifiers only, never PINs,
  tokens, hashes of PINs, or retry counters. Multiple devices require confirmation;
  a serial/model match is not proof that the device owns the SSH credential.
- Wipe owned secret buffers on replacement/expiry; avoid claims of universal
  zeroization of Go/GTK copies. Never bypass hardware presence using a cached PIN.

## Repository and publication

- Keep commits focused and inspect status/diff before staging; preserve unrelated
  user changes. Do not commit test credentials, build products, or trace logs.
- Every assisted commit needs an `Assisted-by:` Git trailer identifying the actual
  model used, e.g. `Assisted-by: OpenAI gpt-6-astra (opencode/gpt-6-astra)`.
- Commit/push/tag/release only within the user's authorization. Do not amend shared
  history or force-push. Use `gh` for GitHub operations.
- CI uses standard public GitHub runners. Keep release permissions confined to
  publishing jobs; fork pull requests must not receive write credentials.
- Pin third-party Actions to commit SHAs and keep Dependabot updates enabled.
- For release/version changes, follow `docs/RELEASING.md`, update the package
  version, and preserve architecture-specific verification and checksums.
- Update design/usage documentation when externally visible behavior changes.
