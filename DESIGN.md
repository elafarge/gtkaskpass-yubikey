# gtkaskpass-yubikey design

Status: **implemented; approved design with implementation notes**.

The design-only phase preceded implementation and was approved by the user.
The existing `prompt.md` records the original request. This revision includes
the subsequent memory-cache, trace-mode, and Apache-2.0 requirements.

## 1. Goal

Build a small Linux SSH askpass executable in Go with a GTK4 interface that:

- Collects SSH private-key passphrases and other SSH secret responses.
- Collects a security-key PIN when OpenSSH requests one.
- Displays a touch notification for a YubiKey or another FIDO security key.
- Automatically closes that notification when OpenSSH ends the signing attempt.
- Reuses a key's passphrase or FIDO PIN from memory for a configurable duration
  (default `1h`), avoiding repeated entry while still requiring hardware touch.
- Integrates easily into NixOS and is structured for eventual nixpkgs packaging.

The askpass executable is `gtkaskpass-yubikey`. A companion executable,
`gtkaskpass-yubikey-cache`, provides the cache daemon and cache-control commands.

## 2. Important protocol constraint: OpenSSH owns the sequence

**An askpass helper cannot implement PIN validation followed by touch detection
as a single request using the standard askpass interface.** It receives prompt
text and an optional UI hint, and returns a response or displays a notification.
It receives neither the signing request nor PIN-validation results.

Three different concepts must be kept distinct:

1. A **private-key passphrase** decrypts a local key file. A FIDO key's local
   key-handle file can also have this protection.
2. A **security-key PIN** performs authenticator user verification. This is what
   “YubiKey passphrase” means in the proposed FIDO flow. Some keys/operations do
   not require a PIN.
3. **Touch / user presence** authorizes an operation on the authenticator.

`Confirm user presence for key ...` requests the third item, not the second. It
also does not identify the authenticator manufacturer.

### 2.1 What OpenSSH actually does

For synchronous input, OpenSSH launches the helper with a prompt in `argv[1]`,
reads stdout, and waits for it to exit. Returning a PIN requires completing this
invocation; keeping its window/process open would block the signing operation.

For notifications, OpenSSH launches the helper with
`SSH_ASKPASS_PROMPT=none`. Its stdin and stdout are connected to `/dev/null`.
OpenSSH continues signing concurrently, then sends the helper **SIGTERM** and
waits for it to exit. A PIN entered into this notification could not reach SSH.

The direct-client FIDO signing path can start a touch notification **before** it
discovers that a PIN is required. It then stops the notification, requests the
PIN through a separate invocation, and starts another signing attempt with a
new touch notification. That new notification does not certify that the PIN has
already been validated.

Example sequence, with every box a separate helper process:

```text
[local key-file passphrase, if required] -> return response and exit
[touch notification, possibly brief]    -> OpenSSH sends SIGTERM
[security-key PIN, if required]         -> return response and exit
[touch notification]                   -> OpenSSH sends SIGTERM
```

The final SIGTERM also occurs on signing failure or abandonment. It means
“notification finished,” not necessarily “touch succeeded” or “SSH login
succeeded.” The UI closes without displaying an inferred success message.

### 2.2 Approved decision

Implement a **standard OpenSSH askpass frontend**, with independent input and
notification windows following OpenSSH's requests. OpenSSH and its security-key
provider validate credentials and perform the hardware operation. The helper
does not open the YubiKey or attempt a second PIN verification/signature.

This delivers PIN entry when requested and an automatically dismissed touch
window. It does **not** guarantee a single persistent window or strict
“validated PIN, then touch” ordering. Requiring either would change the scope
to integration with the signing provider, agent, or OpenSSH itself, because
standard askpass supplies no validation event or cross-request transaction ID.

Initial hardware scope is OpenSSH's native FIDO keys (`ed25519-sk` and
`ecdsa-sk`). PIV/PKCS#11 and GPG-backed SSH setups can use ordinary input prompts
when they invoke askpass, but their touch lifecycle is outside this design's
guarantee. Agent-backed behavior depends on which prompts the agent emits; a
direct-client PIN flow must not be assumed for every agent.

## 3. Invocation and response contract

### Inputs

```text
gtkaskpass-yubikey [prompt]
```

- One argument is the complete prompt, even when it contains spaces or newlines.
- With no argument, use a generic `Enter SSH passphrase:` prompt.
- An explicitly empty prompt uses the same generic display text.
- More than one argument is an invocation error: stderr diagnostic, exit 2.
- Treat the prompt as data, including strings starting with `-`. Initially there
  is no option parser that could mistake prompt text for a flag.
- Do not read a response from stdin.

### Mode selection, in priority order

| Condition | Mode | Presentation |
| --- | --- | --- |
| `SSH_ASKPASS_PROMPT=none` | Notification | Touch UI for a recognized presence prompt; otherwise informational UI |
| `SSH_ASKPASS_PROMPT=confirm` | Confirmation | Explicit Allow / Deny buttons; no secret entry |
| Hint absent/empty, recognized `Confirm user presence for key` prefix | Notification | Touch UI; compatibility fallback |
| Otherwise | Input | Masked response field; PIN-specific title for a recognized PIN prompt |

Unknown nonempty hints fall back to input mode. Known hints take precedence over
text. Recognize the presence prefix only at the beginning with a word boundary,
using a whitespace-normalized copy so the example `Confirm\nuser presence for
key` works. Keep the original prompt for display. Match narrowly rather than
searching for the word “key” or “YubiKey.” PIN recognition only changes wording;
it never adds PIN-format restrictions or performs validation. Separately, the
cache classifier recognizes only key-specific prompt formats described in
section 5.1; recognizing a PIN title alone does not make a request cacheable.

Generic input also permits SSH password, keyboard-interactive, and textual
host-key questions to receive their exact typed response. A host-key question
is not assumed to carry the `confirm` hint: some OpenSSH questions use ordinary
text input, including support for entering a fingerprint.

### Outputs and exit status

| Outcome | stdout | Status |
| --- | --- | --- |
| Input submitted | Exact response followed by one LF | 0 |
| Eligible cache hit | Exact cached response followed by one LF; no input window | 0 |
| Explicitly submitted empty response | One LF | 0 |
| Confirmation allowed | `yes` followed by one LF | 0 |
| Input cancelled or confirmation denied | Empty | 1 |
| Invocation, GTK initialization, or output-write failure | Empty where no write has occurred | 2 |
| Notification finished by SIGTERM | Empty | 0 after graceful shutdown, or signal termination during early startup |

- Escape and window-close cancel an input/confirmation request.
- SIGINT, SIGHUP, and SIGTERM cancel active input without submitting a response.
  A signal before handlers are installed may terminate the process directly;
  both paths are unsuccessful input outcomes for OpenSSH.
- Notifications never write a response, including when dismissed by the user.
- Preserve leading/trailing whitespace and UTF-8 in submitted input.
- Reject CR, LF, and NUL in responses rather than silently changing their value.
- The inspected OpenSSH implementation reads at most 1023 response bytes.
  Limit responses to 1022 UTF-8 bytes plus LF and show an inline length error
  instead of allowing silent truncation. Test byte boundaries, not just character
  counts. This is a compatibility limit, not a PIN-length rule.
- All diagnostics go to stderr. Responses are redacted except in explicitly
  enabled credential-inclusive tracing (section 6.1). A failed write may already
  have emitted a partial response; report failure rather than claiming success.

## 4. GTK4 user experience and lifecycle

### Input window

- Look up eligible requests in the cache before GTK initialization. On a miss,
  bypass, or unavailable daemon, show the input window normally.
- A compact window titled `SSH passphrase`, `Security key PIN`, or `SSH input`.
- Full caller prompt rendered as plain text, wrapped and scrollable for long
  messages; retain key paths and fingerprints.
- A GTK password entry, initially focused, with a reveal control.
- Submit and Cancel buttons; Enter submits, Escape cancels; accessible labels
  and normal keyboard navigation.
- Empty input is allowed on explicit submission because cancellation and an
  empty answer are different protocol outcomes.
- For cacheable requests, show `Remember for <configured duration>` enabled by
  default when the daemon is available and caching is enabled. Unchecking it
  also forgets the existing entry for this key/kind. Generic prompts and bypassed
  requests have no remember option.
- Submitting closes the window, writes the response exactly once, records an
  eligible nonempty response in the cache after a successful write, and exits.
  Cache IPC has bounded deadlines and cannot hold up exit indefinitely. A cache
  failure must not turn successfully supplied input into an authentication error.
  Retry decisions and credential-error messages belong to the calling program;
  a repeated request can bypass/invalidate a cached candidate as in section 5.1.

### Touch notification

- Title `Touch your security key`, original key information, and a waiting
  indicator. Use manufacturer-neutral wording because the prompt is not proof
  that the device is a YubiKey.
- No PIN entry and no button that claims to confirm physical touch.
- SIGTERM closes the window and exits promptly. Register termination handling
  early and handle arrival before presentation as well as during the main loop.
- A Dismiss action, Escape, or window-close hides the notification while leaving
  the process alive until OpenSSH ends it. Keep the GTK application held while
  hidden. Dismissal does not cancel SSH signing; the UI labels it accordingly.
  Keeping the process alive also preserves the PID OpenSSH intends to terminate.
- No arbitrary touch timeout: OpenSSH owns operation timeout and completion.
  A manually launched notification must be terminated by its launcher.
- In notification mode, also watch the lifetime of the original parent (using
  a Linux pidfd where available, with a documented fallback) so a crashed caller
  does not leave an orphaned window or hidden process. Never signal the parent.

### GTK integration

Use `github.com/diamondburned/gotk4/pkg/gtk/v4` and its GLib/GIO bindings, pinned
to a revision compatible with the selected nixpkgs GTK4 version. Use generated
bindings as dependencies; build-time binding generation is unnecessary.

GTK initialization, widget access, and the main loop run on the initial locked
OS thread. Go signal/parent watchers cancel a context; a 50ms GLib timer observes
cancellation and shuts down on the GTK thread. Completion is idempotent so
submit, close, and signals cannot produce
duplicate output or leave a blocked main loop.

Use a non-unique GTK application: each invocation has its own process, window,
stdout, and lifecycle. Concurrent SSH operations share only the credential cache,
not a GTK application instance. GTK handles native Wayland and X11; use
standard theme styling without compositor-specific positioning assumptions.

## 5. Go architecture and layout

Go has no mandatory “standard project layout.” Use the conventional `cmd/` and
`internal/` arrangement with a single module and tests alongside packages:

```text
cmd/gtkaskpass-yubikey/main.go     # process entry point and dependency wiring
cmd/gtkaskpass-yubikey-cache/      # daemon and forget/control subcommands
internal/askpass/                 # request classification, results, wire output
internal/app/                     # request lifecycle and UI interface
internal/cache/                   # identity, expiry, entries, retry tracking
internal/cacheipc/                # Unix-socket client/server and peer checks
internal/gtkui/                   # GTK4 windows and GLib dispatch
internal/lifecycle/               # Linux signals and parent-lifetime handling
tests/integration/               # subprocess, GUI, and OpenSSH tests
tests/testdata/                  # synthetic prompts/fixtures; no real credentials
nix/package.nix                  # standalone callPackage-compatible derivation
nix/module.nix                   # NixOS askpass and user cache-service integration
go.mod
go.sum
flake.nix
flake.lock
README.md
DESIGN.md
LICENSE                         # Apache License, Version 2.0
```

Keep GTK imports at the UI boundary so classification, response encoding, and
controller tests can run without a display or GTK initialization. Use a small UI
interface and injected cancellation/output dependencies for meaningful tests.
Avoid a public `pkg/` API until there is a real external consumer.

The askpass executable remains a short-lived frontend. A separate per-user Go
daemon, with no GTK dependency, retains credentials between invocations. It owns
expiry and concurrent cache access; each askpass process owns its own UI and SSH
response. The daemon receives no signing requests and requires no USB access.

### 5.1 Credential cache

#### Scope and key identity

- Enable reuse of both local private-key passphrases and FIDO PINs. Separate the
  two credential kinds even when they refer to the same key file.
- Cache only recognized OpenSSH key-specific input prompts carrying an
  unambiguous local key-file path or agent SHA256 fingerprint. File prompts include the supported `ssh` and `ssh-add`
  passphrase formats and `Enter PIN for <type> key <path>:`. Pin exact formats in
  tests against the OpenSSH version used in integration tests, including retry
  wording such as `Bad passphrase, try again for <path>:` for `ssh-add`.
- Key entries by credential kind and canonical absolute key-file path, with a
  file-identity/version stamp (device, inode, size, modification/change times)
  to invalidate entries when the file is replaced or modified. Resolve relative
  paths against the helper's inherited working directory and resolve symlinks.
  The daemon independently resolves/stats the path before using or storing an
  entry. Parsing must preserve whitespace inside filenames. Eligible files must
  be regular files owned by the current user. The implementation leaves annotated
  `ssh-add -c` prompts uncached because their suffix is ambiguous with a filename.
- This identity comes from the prompt and local file metadata; it is not a
  cryptographically verified key fingerprint. Do not read/decrypt private-key
  contents to derive it. If the format/path is ambiguous, potentially truncated
  at a known caller formatting limit, unresolvable, or metadata cannot be
  obtained, fall back to an uncached input prompt. File aliases that cannot be
  linked reliably may have separate entries.
- Scope a PIN to the requested SSH key, even if several keys live on one YubiKey.
  Askpass cannot reliably identify the physical device or its shared PIN domain.
- Agent requests use a separate `(pin, SHA256 fingerprint)` namespace. Accept
  only canonical unpadded base64 encodings of exactly 32 digest bytes from
  `Enter PIN [and confirm user presence ]for ED25519-SK|ECDSA-SK key SHA256:...:`.
  The fingerprint is never interpreted as a file path. The helper and daemon
  both validate it; the protocol explicitly distinguishes path and fingerprint.
  Resident keys need no local file. File-based and agent entries are not merged
  by guessing filenames or scanning private keys.
- Never cache generic SSH account passwords, keyboard-interactive answers,
  host-key approvals, empty responses, cancellations, or touch notifications.
  Unknown prompts still work through the ordinary uncached input path.

#### Lifetime and configuration

- Default TTL: **`1h`**, configured on the daemon as a Go duration (for example,
  `15m` or `2h`). `0` disables caching; negative or invalid values are configuration
  errors. NixOS exposes the same setting as `cacheTTL`.
- Expiry is **absolute from storing a manual submission**, not sliding: reading a cached
  entry never extends it. Typing a new response replaces the entry and starts a
  new TTL. Store each entry's deadline explicitly; use Linux elapsed time that
  includes suspend (`CLOCK_BOOTTIME`) so suspend does not extend its lifetime.
- Check expiry on every access and proactively evict expired entries. Clear
  entries on explicit forgetting and daemon shutdown. A daemon restart starts
  empty; changing the service's TTL restarts the daemon and clears existing data.
- Use bounded cache/protocol sizes and prune caller bookkeeping when callers
  exit. Resource exhaustion falls back to prompting rather than retaining
  unbounded data or dropping retry protection.
  Implemented bounds are 256 keys, 4096 caller/key records, and 256 pending
  submissions. Pending submission leases expire after ten minutes; an answer
  submitted after its lease expires is still returned to SSH but is not cached.
- Setting `GTKASKPASS_CACHE=off` on an askpass invocation bypasses both lookup
  and storage. This lets the user force fresh input without reconfiguring the
  daemon. Explicit forgetting is available for one key/kind or the whole cache.

#### Rejected or changed credentials

Askpass receives **no authentication-success/failure callback**. A manually
submitted credential is therefore cached as a candidate, not a validated secret.
The cache never tries to verify a passphrase or PIN itself.

To handle retries, track which entry generation was supplied to each originating
caller process and key/kind, including manually submitted answers. Identify the
caller using the helper's parent PID and process start time, checked against the
connected helper's Linux process metadata; a reused PID is a different caller.
If this identity cannot be established, bypass caching for the request.

For file-based requests, if that caller asks for the same key/kind again, treat it conservatively as a
retry: bypass the cache and show the input window. Invalidate the previously
supplied generation if it is still current. A newer entry from another caller
must not be deleted by an older retry. Repeated requests in this caller remain
interactive; a long-lived caller can consequently prompt more often than
separate `ssh` processes. This trades some reuse for avoiding a rejected-answer
loop when no transaction/result information exists.

For agent fingerprint prompts, OpenSSH asks for a PIN at most once per signing
operation; later requests from the same long-lived agent are separate operations.
They may reuse the cached candidate, including forwarded-agent signing requests.
Do not record these operations in the file-based retry map. Submission leases,
generation checks, size bounds, and absolute TTL still apply. The agent owns
hardware verification and touch. An incorrect cached PIN must be explicitly
forgotten; never infer validation or rejection from notifier/caller termination.

A caller can exit after rejecting a PIN without requesting it again. In that
case the helper cannot detect rejection, and a later caller can receive the
candidate again until expiry or explicit forgetting. Notification termination
and caller exit are not validation signals. Document `forget` and cache bypass
for this case, rather than claiming fully automatic invalidation of bad PINs.

Concurrent requests may independently prompt on a miss; do not merge unrelated
SSH dialogs. Use daemon-issued generations and conditional stores/invalidations
so stale requests cannot overwrite newer entries or resurrect credentials after
`forget`. A credential already returned to a helper cannot be recalled.

#### Daemon and IPC

- Use one daemon per UID and a Unix-domain socket at
  `$XDG_RUNTIME_DIR/gtkaskpass-yubikey/cache.sock`, inside a user-owned `0700`
  directory; socket mode `0600`. Validate runtime-directory ownership instead
  of falling back to a shared temporary directory.
- Both sides check peer UID with `SO_PEERCRED`. Requests are versioned,
  length-bounded, and deadline-limited. Operations cover lookup, conditional
  store/invalidation, forgetting, and nonsecret configuration metadata for the UI.
  Protocol v1 uses length-prefixed JSON frames of at most 16 KiB, a two-second
  I/O deadline, and at most 64 concurrent connections.
  IPC secrets are carried only in the socket payload, never in argv, environment
  variables, daemon logs, or cache-control output. The helper's explicit
  credential-inclusive trace can additionally print its response to stderr
  (section 6.1).
- This is a per-user trust boundary: other processes running as the same user
  can act as clients. The protocol does not claim isolation from that user's
  other programs or from root.
- Prefer **systemd user socket activation**. The socket unit starts the daemon
  on first eligible use, and systemd serializes concurrent startup. The service
  runs in the foreground and accepts the socket passed by systemd. It requires
  no graphical-session environment and does not inherit an askpass stdout pipe.
- Also support foreground `serve` with an exclusively owned socket for Linux
  installations without a user systemd manager. Do not unlink a live socket or
  spawn an unmanaged daemon from every helper. Packaging supplies service units;
  NixOS integration enables them automatically when requested.
- If the service is not configured, the socket is unavailable, or communication
  fails, continue with an ordinary uncached GTK prompt and a concise stderr
  diagnostic. Cache unavailability must not prevent SSH use. Bypass/noncacheable
  requests do not activate the daemon.
- A normal user-service stop/restart clears the cache. On a lingering systemd
  user manager, logout may not stop it; expiry remains authoritative. Document
  explicit forgetting on logout if that behavior is desired.

Proposed companion commands (separate from the prompt-only askpass executable):

```sh
gtkaskpass-yubikey-cache serve --ttl 1h
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519 --kind passphrase
gtkaskpass-yubikey-cache forget --key ~/.ssh/id_ed25519_sk --kind pin
gtkaskpass-yubikey-cache forget --fingerprint SHA256:PUBLIC_KEY_FINGERPRINT
gtkaskpass-yubikey-cache forget --all
```

The foreground command is for manual/service use; normally socket activation
starts it. Forget-by-path removes all stored versions of that key/kind and must
still work if the file has since been removed. Forgetting an empty/stopped cache
is a successful no-op; no command displays a cached secret.
`--fingerprint` forgets only that agent PIN entry and is mutually exclusive with
`--key`, `--kind`, and `--all`. Existing protocol-v1 daemons reject the new
fingerprint request shape and helpers fall back to prompting; upgrade/restart
the daemon together with the frontend to enable the new cache namespace.

#### Memory handling and relationship to ssh-agent

Keep entries in daemon-owned byte buffers, wipe those buffers on eviction or
replacement, disable daemon core dumps, and avoid serialization to disk. Minimize
copies of secret values in the helper and clear the GTK entry when finished.
“Memory-only” means no persistent application credential store; Go/GTK transient
copies and OS swap mean this does not promise that every copy is unswappable or
securely erasable.

For software keys, OpenSSH's existing `ssh-agent` can already avoid repeated
passphrase entry by retaining the decrypted key with a lifetime. It does not
provide a general askpass PIN cache, so this design adds the requested cache
while remaining compatible with an existing agent. It does not change agent
settings or add identities to the user's agent automatically. Cached PINs never
satisfy or bypass hardware user-presence requirements.

## 6. OpenSSH and desktop integration

Document configuring an absolute executable path:

```sh
export SSH_ASKPASS=/absolute/path/to/gtkaskpass-yubikey
export SSH_ASKPASS_REQUIRE=prefer
```

`SSH_ASKPASS_REQUIRE` is interpreted by OpenSSH; the helper does not select
whether SSH uses a terminal. Use `force` for deterministic input tests. Do not
set `SSH_ASKPASS_PROMPT` globally: callers supply this per invocation.

**Touch notifications have a separate terminal rule.** In the inspected
OpenSSH implementation, `notify_start()` normally writes to stderr when stderr
is a terminal. `SSH_ASKPASS_REQUIRE=force` alone does not override that first
branch. A graphical launch with non-terminal stderr, or an explicitly documented
stderr redirection for a terminal test, is needed to exercise the notification
helper. Batch mode must not be suggested as a workaround: it also changes
authentication prompting.

GTK needs the graphical session environment, including the appropriate display
variables and, for Wayland, `XDG_RUNTIME_DIR`. Agents started outside the desktop
session need this environment and `SSH_ASKPASS` propagated to their own process.
The cache additionally needs the user's real `XDG_RUNTIME_DIR`. Setting only
`SSH_ASKPASS` installs the frontend behavior; enable the supplied cache socket
service (or run the foreground daemon) to retain credentials between invocations.

### 6.1 Trace mode for troubleshooting

Tracing is opt-in and writes exclusively to **stderr**, keeping stdout byte-for-
byte compatible with askpass. Configure the helper with `GTKASKPASS_TRACE`:

| Value | Behavior |
| --- | --- |
| Unset or `off` | Normal diagnostics only; no request/response trace |
| `metadata` | Trace inputs, decisions, and outcomes; redact credential values |
| `secrets` | Same trace plus exact submitted/returned credential values, escaped for display |

Trace records include timestamp, helper PID/request ID, and event name. Record:

- Incoming prompt argument and `SSH_ASKPASS_PROMPT`, preserving their distinction
  from the normalized classification text; record only explicitly relevant
  environment settings, not a dump of the full process environment.
- Selected UI mode, cache eligibility/key identity, hit/miss/bypass/retry/file
  changes, and fallback reasons. An expired entry produces a miss. Include
  daemon-issued submission tokens where available for correlation.
- UI submit/cancel/dismiss, signals and parent exit, and notification shutdown.
- Outgoing response byte count, stdout-write success/failure, and exit status.
  Distinguish an intended write from bytes actually written on a partial failure.
  Confirmation answers can be shown in metadata mode; all input-mode answers,
  including PINs, passphrases, and cache hits, are redacted there. Empty output
  and an explicitly submitted empty response remain distinguishable.

In `secrets` mode, show the response with quoted escaping, including its final
LF, so whitespace and encoding issues can be inspected without injecting control
sequences into the terminal. Apply the same escaping to prompts in both modes.
Capture submitted values only on submission, not individual keystrokes or text
left in a cancelled window. This mode puts credentials into stderr and any file
or journal receiving it; enable it explicitly for the troubleshooting invocation.
Neither trace mode writes a log file itself or dumps the daemon's cache contents.

Examples:

```sh
GTKASKPASS_TRACE=metadata SSH_ASKPASS_REQUIRE=force ssh example-host
GTKASKPASS_TRACE=secrets SSH_ASKPASS_REQUIRE=force ssh example-host
```

These assume `SSH_ASKPASS` is already configured. The separate stderr-terminal
rule for touch notifications still applies. For agent-originated askpass calls,
the setting must be in the agent's environment; exporting it only in the SSH
client cannot configure an already running agent.

The daemon supports `serve --trace` for metadata-only lifecycle/IPC/cache events
on its own stderr (the user journal under systemd). It never logs credential
payloads. A helper's trace setting applies to that helper only and is not silently
propagated to the shared daemon. Cache-control diagnostics follow the same
metadata-only rule.

Trace output is best-effort: a stderr-write failure does not change the askpass
answer or exit status. Add coverage for trace failure alongside output-failure
tests, and keep trace writes out of the cache's critical sections.

## 7. Nix and NixOS packaging

- Put the package recipe in `nix/package.nix`, usable with `pkgs.callPackage`
  independently of flakes to ease a future nixpkgs submission.
- Use `buildGoModule`, a real pinned `vendorHash`, CGO, `pkg-config`, GTK4, and
  `wrapGAppsHook4`; add only native dependencies required by the chosen bindings.
- Commit `flake.lock` and Go dependency checksums. Build and checks run without
  downloading dependencies during the sandboxed build phase.
- Export `packages.<system>.gtkaskpass-yubikey`, `packages.<system>.default`,
  `apps.<system>.default`, `devShells.<system>.default`, and `checks.<system>`.
- Export `nixosModules.default` to configure the askpass executable and systemd
  user cache socket/service. Expose `services.gtkaskpass-yubikey.enable`,
  `package`, and `cacheTTL` (default `"1h"`, `"0"` to disable caching). Keep the
  daemon headless and install both executables in the package.
- Target `x86_64-linux` and `aarch64-linux`; verify natively where builders are
  available and distinguish configured targets from actually tested targets.
- The development shell supplies Go, compiler/pkg-config, GTK dependencies, and
  integration-test tools. The wrapped installed executable must work outside
  that shell, with themes and icons available.
- Provide package metadata, `mainProgram`, Linux platforms,
  `meta.license = lib.licenses.asl20` (Apache-2.0), and the public repository
  homepage at `https://github.com/elafarge/gtkaskpass-yubikey`.

Example consuming NixOS configuration with caching (the flake input is named
`gtkaskpass-yubikey`):

```nix
{ inputs, ... }: {
  imports = [ inputs.gtkaskpass-yubikey.nixosModules.default ];
  services.gtkaskpass-yubikey = {
    enable = true;
    cacheTTL = "1h"; # default; for example, change to "15m"
  };
}
```

The module sets the existing `programs.ssh.enableAskPassword` and
`programs.ssh.askPassword` options and adds the user cache units. With `cacheTTL`
set to `"0"`, configure the helper for cache bypass and omit the cache units.
Manual package consumers can still set those SSH options and manage the daemon
themselves. Document `SSH_ASKPASS_REQUIRE` separately as a session preference
rather than silently forcing GUI prompts system-wide.

## 8. Verification strategy

### Unit and controller tests

- Table-driven prompt/hint classification: precedence, missing/unknown hints,
  whitespace-normalized presence prefixes, lookalike strings, PIN prompts,
  Unicode, empty prompts, and arbitrary host-key/input prompts.
- Byte-exact output, whitespace preservation, explicit empty submission,
  confirmation approval/denial, forbidden delimiters, and UTF-8 length limits.
- Cancellation and write errors, including short writes; exactly-once completion
  under competing events.
- Notification dismissal versus termination, early termination, parent exit,
  and independent concurrent requests.
- Cache identity parsing, path resolution, separate PIN/passphrase namespaces,
  key-file replacement, and noncacheable/ambiguous prompts.
- A fake elapsed-time clock for exact TTL boundaries, non-sliding expiry,
  suspend-equivalent elapsed time, replacement, disable/bypass, and forgetting.
- Caller identity/PID reuse, rejected-candidate retry bypass, conditional
  invalidation, concurrent stores, forget-versus-in-flight-store races, and
  bounded bookkeeping. Never test PIN failures against real hardware by default.
- IPC size/version/deadline handling, peer credentials, socket/directory
  permissions, daemon loss, and buffer clearing on eviction.
- Trace defaults, escaping, credential redaction for both manual input and cache
  hits, explicit credential-inclusive output, partial stdout writes, and failure
  of the trace writer. Verify that neither daemon tracing nor ordinary error
  paths print credential payloads.

### Executable and GTK integration tests

Run the **actual executable** under a private D-Bus session and Xvfb, with a
minimal window manager when needed for focus and close events. Use external
keyboard/window automation to interact with real GTK widgets and capture stdout,
stderr, exit status, and window/process lifetime.

Cover typing/submission, Cancel, Escape, window-close, confirm/deny, unavailable
display, malformed invocation, literal markup-like prompts, and concurrent
requests. A small Go parent harness emulates OpenSSH's notification invocation,
including `none`, `/dev/null` I/O, SIGTERM, early termination, and parent death.
Assert that dismissal hides the window while the process remains alive until
completion or parent exit, and that it produces no response. Include the
touch -> PIN -> touch sequence and an error-completion sequence without equating
termination with successful touch.

Run cache-enabled cases with an isolated runtime directory and real daemon:
first request prompts, a new caller reuses the answer without a window, repeated
requests from the same caller bypass it, another key/kind prompts, and expiry or
forgetting restores the input window. Verify restart empties the cache, daemon
failure falls back to input, and cached PIN reuse still leaves notification
lifecycle unchanged. Test the headless hit path without a display. Use a
systemd-capable NixOS test to verify user socket activation, concurrent first
connections, module TTL configuration, and service restart with the installed
package.

Run representative input/cache-hit/cancel/notification cases with tracing off,
in metadata mode, and in secrets mode. Assert identical stdout and exit behavior,
and inspect stderr using synthetic credentials to verify the documented
redaction/inclusion rules. Include multiline and control-character prompts.

Use readiness conditions and bounded deadlines instead of fixed sleeps. Keep
test answers synthetic. Production builds have no test-only environment variable
or flag that injects a secret; normal cache hits are the documented way to return
a previously entered response without a window.

### Real OpenSSH compatibility tests

Create a temporary encrypted software key using `ssh-keygen` and an isolated
`ssh-agent`, then use `ssh-add` with this executable and forced askpass. Drive
the GTK window externally and verify that the expected public key reaches the
agent. Exercise an incorrect response followed by a retry and cancellation.
Keep HOME, agent socket, keys, and process cleanup isolated from the user's SSH
configuration and running agent.

Repeat `ssh-add` from a fresh caller after removing the test identity from the
isolated agent, proving the askpass cache supplies the passphrase without UI.
After expiration or forgetting, assert that manual entry is required again.
Change the temporary key's passphrase and verify file-version invalidation;
also seed an incorrect candidate via ordinary test UI interaction to exercise
OpenSSH's actual re-prompt path and ensure the same answer is not replayed in a
loop. These cases test the cache independently of the agent's own key retention.

These tests prove a real caller can consume the executable's result. The
notification harness proves the documented lifecycle independently of hardware;
it must not be described as validation against a physical YubiKey.

The agent regression additionally uses a test-only OpenSSL software FIDO provider
implementing OpenSSH's provider ABI. It creates disposable keys and requires a
synthetic PIN, causing the real `ssh-agent` to emit both supported PIN prompts.
Repeated `ssh-add -T` requests verify valid signatures and same-agent PIN reuse.
A local Paramiko SSH test server then requests signing through a real OpenSSH
agent-forwarding channel and verifies that it hits the same cache entry. The
provider and server are never installed with the application and do not access
hardware or the user's agent. These tests demonstrate protocol compatibility,
not physical-touch verification.

### FIDO and desktop acceptance

Document opt-in tests using a disposable FIDO SSH identity and a configured test
server: PIN-required signing, touch-only signing, touch dismissal on completion,
operation failure/device removal, and simultaneous requests. Report what was
actually run; hardware tests require an available token and physical interaction.
Use the normal provider's failure handling instead of repeated automatic bad-PIN
attempts. Verify native Wayland presentation/lifecycle as well as automated X11
tests; automate a nested Wayland compositor smoke test where the environment
supports it.

### Required checks during implementation

- `gofmt`, `go vet ./...`, and `go test ./...` in the development environment.
- `golangci-lint run`, configured by `.golangci.yml`, checks production code and
  integration-tagged tests. It is also part of the Nix package check phase.
- Race-detector tests for the pure-Go controller/protocol/lifecycle/cache/IPC
  packages, including concurrent cache clients.
- Explicit integration target for GUI and real OpenSSH tests; Nix checks invoke
  it with the necessary display/session tools. Missing prerequisites must be
  visible, and required Nix checks must not silently skip these suites.
- `nix build` and `nix flake check`, including a smoke test of the installed,
  wrapped executable outside the development shell.

## 9. Repository and implementation milestones

Implementation is organized into focused commits around these milestones:

1. Approved design, Apache-2.0 license, and repository/module foundation.
2. Askpass contract, classifier, controller, and their unit tests.
3. GTK4 input/confirmation windows and subprocess/GUI coverage.
4. Touch-notification lifecycle and signal/parent/concurrency tests.
5. Memory cache, expiry, identity/retry rules, daemon/IPC, and unit tests.
6. Frontend cache integration, forget commands, and end-to-end cache coverage.
7. Trace modes, real OpenSSH integration coverage, and usage documentation.
8. Reproducible Nix package, user socket/service, module, flake checks, and NixOS
   documentation.

Introduce a development flake earlier if needed to build GTK, then complete
packaging/checks in the final milestone. Keep the design updated when an
implementation decision changes. Preserve and inspect pre-existing user files
before deciding whether to include them in a commit.

Every commit made by this assistant will include this Git trailer, separated
from the commit body by a blank line:

```text
Assisted-by: OpenAI gpt-6-astra (opencode/gpt-6-astra)
```

Use concise imperative commit subjects. Initial implementation stayed local;
the user subsequently authorized public publication under `elafarge` and CI/CD.
If Git author identity is missing, request it rather than inventing an identity
or changing Git configuration.

### 9.1 Public CI/CD

GitHub Actions uses standard public x86-64 and ARM Linux runners with read-only
permissions for pull requests and ordinary checks. Both architectures run the
native package/lint/unit/race and GUI checks; the KVM-dependent NixOS VM runs on
x86-64. Third-party Actions are pinned to commit SHAs and tracked by Dependabot.

Version tags matching the Nix package version trigger a checked release pipeline.
Native runtime closures and checksums are uploaded directly to an unpublished
GitHub Release. The final publishing job verifies both architectures, source
revisions, and GitHub's asset digests before making it public. Write permissions
are limited to release publishing jobs. No Actions artifact/cache storage is used.

Public GitHub Packages is free but lacks a native Go/Nix registry. GitHub Releases
fits this application's prebuilt Nix distribution. Cost references and the exact
release/installation procedure are maintained in `docs/RELEASING.md`.

### 9.2 License decision

The user selected **Apache License, Version 2.0** (`Apache-2.0`) for this project's
code and documentation. The full license text is in `LICENSE`; project metadata
uses the same SPDX identifier.
The copyright owner is Étienne Lafarge, explicitly recorded in `LICENSE`.
Third-party dependencies retain their own licenses and required notices.

## 10. Review points and confirmed decisions

1. **Approved OpenSSH-driven flow in section 2:** separate PIN and touch
   invocations, no helper-side validation, and notification termination rather
   than direct touch detection. This is the central scope decision.
2. **Hardware path:** native FIDO SSH keys, as scoped in the approved design.
3. **Cache semantics:** per-key passphrases and PINs, one-hour absolute default
   TTL, headless per-user daemon, and explicit forgetting. Cached responses are
   candidates: file-based same-caller re-prompts bypass them; agent prompts can
   reuse them across operations and require explicit forgetting after rejection
   because failures cannot be detected through askpass alone (section 5.1).
4. **License (confirmed):** Apache-2.0 for project code and documentation.
5. **Go module path:** `github.com/elafarge/gtkaskpass-yubikey`, updated from the
   initial local module path when public publication was authorized.

Implementation verification and physical-token acceptance steps are recorded in
`tests/README.md`. The application uses gotk4 v0.4.1 with the pinned Nix GTK4
environment. Python is used for Wayland, NixOS VM, and forwarded-agent test drivers;
both installed executables and the main integration harness are written in Go.

## 11. Protocol and dependency references

Inspected for this draft on 2026-09-18. Pin the build's dependency revisions and
record the OpenSSH version exercised by integration tests during implementation.

- [OpenSSH portable `readpass.c`, 10.2p1](https://github.com/openssh/openssh-portable/blob/V_10_2_P1/readpass.c):
  `ssh_askpass`, `ask_permission`, `notify_start`, and `notify_complete`; argv,
  output handling, hints, terminal routing, and SIGTERM notification lifetime.
- [OpenSSH portable `sshconnect2.c`](https://github.com/openssh/openssh-portable/blob/master/sshconnect2.c):
  `identity_sign`; direct-client FIDO notification/PIN ordering.
- [OpenSSH `ssh(1)`](https://man.openbsd.org/ssh.1): `SSH_ASKPASS` and
  `SSH_ASKPASS_REQUIRE` environment behavior.
- [gotk4](https://github.com/diamondburned/gotk4) and
  [examples](https://github.com/diamondburned/gotk4-examples): Go GTK4 bindings
  and native build requirements.
- [NixOS SSH module](https://github.com/NixOS/nixpkgs/blob/nixos-unstable/nixos/modules/programs/ssh.nix):
  `programs.ssh.enableAskPassword` and `programs.ssh.askPassword` integration.
