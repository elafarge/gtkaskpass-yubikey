# Contributor and coding-agent notes

## Project and layout

- Linux-only Go application using GTK4 through gotk4 and CGO.
- Read `DESIGN.md` for protocol/architecture decisions and `README.md` for usage.
- `cmd/gtkaskpass-yubikey`: short-lived frontend; `cmd/gtkaskpass-yubikey-cache`:
  headless, per-user credential-cache daemon and control commands.
- Keep protocol/controller/cache code under `internal/` independent of GTK.
  GTK imports belong in `internal/gtkui` and frontend wiring.
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
- OpenSSH owns validation and signing. PIN input and touch notifications are
  separate invocations; `none` notifications finish on SIGTERM, including failures.
  Never infer successful authentication from notification termination.
- Dismissing a notification hides it without ending its process; parent death and
  SIGTERM still close it. Input cancellation returns nonzero without a response.
- GTK must run on the initial locked OS thread, with widget access on its main
  context. Each helper is a non-unique application with an independent window/stdout.

## Cache invariants

- Keep all retained credentials in memory. No secret files, argv, environment
  transport, daemon log payloads, or command to dump cached values.
- Separate PINs and passphrases per canonical key file and metadata version.
  Unknown/ambiguous prompts and non-key credentials remain uncached.
- TTL defaults to one hour, is absolute, and includes suspend. Hits never refresh it.
- Cache responses are candidates, not validated credentials. Preserve same-caller
  retry bypass and generation-checked stores/invalidations. Concurrent or forgotten
  entries must not be overwritten/resurrected by a stale request.
- Validate Unix socket/directory ownership and modes, check peer UID, bound frame
  and cache sizes, and keep IPC deadlines. Cache failures fall back to input.
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
