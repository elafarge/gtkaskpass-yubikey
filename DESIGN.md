# gtkaskpass-yubikey design

Linux-only SSH askpass in Go, with GTK4 UI workers and a per-user headless
request service. Code and documentation are Apache-2.0, copyright Étienne Lafarge.

## Responsibilities and processes

```text
OpenSSH / ssh-agent
        |
        v
askpass adapter ---- private Unix socket ---- request service
                                                | prompt classification
                                                | credential cache
                                                | FIDO2 PIN verification
                                                | device preferences
                                                v
                                          per-request GTK worker
```

Three installed executables have separate roles:

- `gtkaskpass-yubikey` is a short-lived protocol adapter without GTK or FIDO
  dependencies. It forwards the original prompt/hint/session information,
  observes its parent's lifetime and termination signals, and owns SSH stdout.
- `gtkaskpass-yubikey-cache` runs the headless service and cache-control commands.
  It owns request state, classification, cache policy, device verification, and
  worker lifetimes. The name is retained for service/configuration continuity.
- `gtkaskpass-yubikey-ui` renders views and returns user actions through an
  inherited private socket pair on descriptor 3. It has no credential-cache or
  hardware logic and is not an interactive command-line entry point.

GTK runs on the worker's initial locked OS thread. A GLib timer consumes incoming
views on the main context; widget callbacks send actions. Each request has an
independent non-unique GTK application, so unrelated sessions cannot steal each
other's prompts. A worker crash ends its request, not the service or its cache.

The service starts a fixed sibling worker executable, never a caller-selected
program. Nix wraps the worker with the GTK runtime environment. Only explicit
display/session variables and locale cross from the adapter: DISPLAY,
WAYLAND_DISPLAY, XAUTHORITY, XDG_RUNTIME_DIR, DBUS_SESSION_BUS_ADDRESS, LANG,
LC_ALL, LC_CTYPE, XDG_ACTIVATION_TOKEN, and DESKTOP_STARTUP_ID. The supplied
runtime directory must match the service's runtime. No caller PATH, loader
variables, GTK modules, or arbitrary environment is forwarded. Service-owned
GSK_RENDERER/GDK_BACKEND/GTK_A11Y settings can configure rendering/testing.

Agent requests display in the local agent's graphical session, including when
the request originated through forwarding. Askpass cannot infer a remote user's
desktop from an agent request.

The same service owns an independent, read-only USB FIDO touch monitor. Its
device notifications have no askpass parent or SSH dependency and use the
service's owning graphical-session environment. The service starts with
`graphical-session.target`, remains running for that session, and stops with it.
Its private request socket is an IPC endpoint, not a socket-activation trigger.
One UID has one owning notification session; incoming requests do not replace
that environment or redirect passive device popups to another desktop.

## OpenSSH protocol

The adapter accepts zero or one prompt argument. A missing/empty prompt gets a
generic input label. More arguments fail; leading `-`, whitespace, and newlines
in a prompt are data, never executable options. Input does not come from stdin.

Known `SSH_ASKPASS_PROMPT` hints take precedence:

| Hint/prompt | Behavior |
| --- | --- |
| `none` | Notification; no stdout response |
| `confirm` | Allow/Deny, with `yes\n` for Allow |
| No hint, recognized presence prefix | Touch notification |
| Otherwise | Input; masked entry, with a PIN-specific title when recognized |

Presence classification normalizes whitespace in a copy; display retains the
original text as plain text rather than markup. Unknown hints fall back to input.

Submitted input is returned exactly, followed by one LF. Explicit empty input is
one LF. Input cancellation/denial returns no bytes and status 1; invocation,
service, UI, or output failures return status 2. No successful response follows
cancellation. Notification termination produces no response. Enforce UTF-8,
reject embedded NUL/CR/LF, and limit responses to 1022 bytes plus LF to fit the
OpenSSH reader. Short/failed stdout writes are failures, not successful delivery.

The adapter acknowledges successful stdout delivery. Only then may the service
commit a newly entered credential; losing that acknowledgment can lose a cache
opportunity but cannot cause a second response. Receiving EOF after request
acceptance never triggers automatic request replay or a second PIN attempt.

The request service is required, even when caching is disabled. Unavailable or
incompatible services fail with a diagnostic; there is no silent unverified PIN
fallback. Compatibility PIN mode is an explicit service configuration.

## Request lifecycle and IPC

The per-user socket is `$XDG_RUNTIME_DIR/gtkaskpass-yubikey/cache.sock`. Runtime
and service directories must belong to the current UID and have mode 0700;
the socket has mode 0600. Both ends check SO_PEERCRED. This is a per-user trust
boundary, not isolation from root or other programs running as the same user.

Frames are length-prefixed JSON, bounded to 16 KiB. Protocol-v2 `ask` requests
carry argv, hint, the allowlisted session environment, cache bypass, and a
metadata-trace switch. The server replies with acceptance, metadata events, and
a result; the adapter replies `delivered`, followed by service completion.
Protocol-v1 cache controls remain supported for existing forget clients.
PIN values travel only in private IPC/stdout payloads, never argv or environment.

Handshake and frame-write deadlines are two seconds. After acceptance, waiting
for the user has no arbitrary timeout. UI startup/readiness and delivery
acknowledgment are bounded. Limit the server to 64 concurrent connections, and
one worker per interactive request. Metadata/UI events are bounded like results.

Caller identity is derived from the adapter peer's parent PID and process start
time. Relative filenames resolve against that caller's working directory, not
the service's directory. PID reuse does not reuse a caller identity.

- Adapter SIGTERM/SIGINT/SIGHUP or parent death closes its request connection.
- Disconnect cancels service work and kills/reaps the worker.
- UI cancellation suppresses a late verification response. Device calls have
  finite timeouts; GTK remains responsive while the service verifies.
- Service shutdown cancels requests, stops workers, and wipes retained secrets.
- A completed request closes its worker before returning its result, preventing
  a stale window from being mistaken for a subsequent prompt.

## Credential cache

The cache owns byte buffers in memory. No credential files, dumps, or daemon
payload logs exist. Clear owned buffers on replacement, expiry, and forgetting.
Go/GTK/native-library temporary copies and OS swap prevent a universal claim of
secure erasure or unswappable memory. Disable daemon core dumps.

Default TTL is one hour, configurable using a Go duration. `0` disables cache
storage, not the request service. TTL is absolute from storing a manual answer;
cache hits and successful re-verification never extend it. CLOCK_BOOTTIME includes
suspend. Expiry is checked on access and proactively swept.

File-based credentials are separated by canonical path, kind (passphrase/PIN),
and device/inode/size/mtime/ctime metadata. Only regular user-owned files qualify.
Agent PINs use a separate canonical SHA256 public-key-fingerprint namespace. The
fingerprint is unpadded canonical base64 encoding exactly 32 bytes, never a path.
Resident agent keys need no corresponding local file. Do not merge file and
agent namespaces by guessing matching public-key files.

Known formats include OpenSSH file-passphrase/retry prompts, file FIDO PIN
prompts, and the agent variants `Enter PIN for ...` and `Enter PIN and confirm
user presence for ...`. Unknown/ambiguous prompts, potentially truncated paths,
annotated `ssh-add -c` prompts, account passwords, confirmations, empty answers,
and notifications are not cached.

File-based repeated requests from the same caller bypass a previously supplied
candidate, with generation-checked invalidation. Agent fingerprint prompts allow
reuse from the persistent agent: OpenSSH asks once per signing operation, and a
later prompt is an independent operation, including forwarded Git requests.

Submission tickets/generations prevent a concurrent replacement or forget from
being overwritten by stale work. Bound entries to 256 keys, caller records to
4096, and pending submissions to 256; submission tickets expire after ten minutes.
Longer user interaction still returns a valid answer but may not cache it.
Resource exhaustion prompts rather than growing memory without bound.

Verified PIN entries additionally bind to the selected device's live connection
identity. A changed connection cannot receive the cached PIN: switching devices
starts with fresh input. Recheck the cache generation before and after hardware
verification. Explicit PIN-invalid results invalidate only the generation used.

`GTKASKPASS_CACHE=off` bypasses lookup/storage for that adapter request, not PIN
verification. For agent requests it must be in the agent's environment. Forget
controls support a file/kind, an agent fingerprint, or all credentials. None
displays values. Restart starts with an empty credential cache.

## FIDO2 discovery, selection, and verification

The default `pinVerification = "required"` mode verifies recognized PIN input
using libfido2 1.17.0. Generic passwords and private-key passphrases never go to
hardware. The explicit `"off"` mode preserves unverified compatibility with
non-device/test providers; those answers remain candidates, and rejected cached
PINs must be forgotten manually. Required mode never silently downgrades.

Discovery enumerates at most 32 accessible FIDO2 devices with a configured PIN,
queries available retry counts, and presents their manufacturer/model and a
serial or live device path as a distinguishing label. Retry-query failure is
unknown, not zero; built-in biometric UV retries are not PIN retries.

- One eligible device: automatic selection, with its identity shown in PIN entry.
- Multiple devices: always show a dropdown and require confirmation, even for a
  cache hit. Preselect only a unique match to the saved preference.
- No device: show Connect your security key, refresh periodically, and allow Cancel.
- PIN entry offers Change device, clears typed input, and restarts selection.
- A disappeared/changed device aborts that attempt; the user can cancel and
  start again. Never submit a previous device's PIN to its replacement.

The live binding uses Linux hidraw/sysfs identity plus device-node metadata and
USB connection numbering, not merely a reusable `/dev/hidrawN` path. A missing
reliable connection identity prevents verified-mode submission. Devices without
unique serials can be selected for the current connection, but cannot be reliably
preselected across reboots. When identical labels are ambiguous, users can
disconnect the unintended device. AAGUID is not a unique physical identifier.

Each verification opens a fresh device handle, sets a three-second device-call
timeout, checks the connection identity again, and performs
`fido_dev_get_puat(dev, FIDO_PUAT_GETASSERT, "ssh:", pin)`. This requests a
PIN-authenticated token scoped to the SSH application; it performs no assertion
or signing operation. libfido2 chooses CTAP2.1 permission-scoped or CTAP2.0 token
acquisition based on device capabilities. Pass a non-null PIN; never substitute
biometric verification or reuse an existing token. Clear the acquired token and
close the handle before returning an answer to OpenSSH.

Outcomes:

- **Accepted:** return the PIN; cache after successful adapter delivery if enabled.
- **PIN invalid:** clear/invalidate that candidate, refresh available retry count,
  show an inline error, and require another explicit user submission. Never
  forward the rejected PIN to OpenSSH or retry automatically.
- **PIN-auth temporarily blocked / PIN blocked:** remove the candidate and show
  the recovery state. No automatic reset, PIN change, or retry after reconnect.
- **Busy, timeout, disconnected, unsupported, or access error:** do not call it
  a bad PIN. Show an error and terminate on cancellation; never return an
  unverified candidate in required mode.

The service serializes hardware-backed requests conservatively. This includes
selection/input so queued requests cannot concurrently retry a stale candidate.
Other input/notification requests remain independent. The lock coordinates only
this service, not OpenSSH, browsers, or other programs. Finite calls and releasing
handles before handing control back to SSH reduce contention but cannot eliminate
it. No hardware operation is started from a touch-only notification.

## Persistent device preferences

Only public device-selection metadata persists at
`$XDG_CONFIG_HOME/gtkaskpass-yubikey/devices.json` (default
`~/.config/gtkaskpass-yubikey/devices.json`). The service owns this versioned JSON
file and serializes writes, with a private 0700 directory and 0600 file, bounded
reads, target checks, a same-directory temporary file, fsync, and atomic rename.
Reject malformed/unsupported documents and symlink targets rather than silently
overwriting them. Preference write failure does not fail authentication.

The file maps canonical SSH fingerprints to scheme-tagged stable USB identities,
derived from vendor/product/serial. A preference is saved after successful
verification and response delivery; remembered submissions additionally require
their generation-checked cache store to succeed. Disabling credential retention
does not disable public device preferences.
No serial means no persistent preselection. Duplicated serial matches are
ambiguous and do not preselect a device. The chooser remains mandatory whenever
multiple eligible devices are connected.

No PIN, PIN hash, token, retry count, or verified state is persisted. Serial-derived
identifiers and fingerprints can still identify users/devices, so permissions
remain private. File-only key prompts have no reliable fingerprint association;
their device choice remains request-local. Remove the preference file while the
service is stopped to reset saved choices; this does not reset a hardware token.

## Passive touch monitoring and success boundaries

OpenSSH owns SSH signing, device selection for signing, hardware presence, and
server authentication. PIN acceptance proves only that the selected device
accepted that PIN at that time. It does not prove the device holds the requested
key or that OpenSSH will choose it. The askpass protocol cannot return a device
selection alongside the PIN.

`internal/touch` watches all accessible USB FIDO HID interfaces, independently
of PIN support or SSH requests. Discovery reads kernel report descriptors under
sysfs and rechecks descriptor/device metadata using read-only hidraw ioctls.
Recognize application usage page F1D0/usage 1, respecting report IDs, lengths,
global push/pop, and collection boundaries. Ambiguous/malformed descriptors are
not interpreted. Devices are opened O_RDONLY|O_NONBLOCK|O_CLOEXEC|O_NOFOLLOW.
No HID writes, feature requests, CTAP commands, PIN checks, or extra assertions
are issued by this component. Linux fans input reports out to separate readers;
monitoring does not steal the signing application's response.

Rescan kernel metadata every second for startup/hotplug/ACL changes and poll
open handles between scans. Bound watched devices to 32, metadata size to 4096
bytes, reports to 1025 bytes, scan entries to 256, and active channels per device
to 32. Clear raw report buffers after inspection and never log packet payloads.
The retained state contains only device metadata, channel IDs, and deadlines.

A valid CTAPHID_KEEPALIVE/UPNEEDED starts or refreshes a pending channel. PROCESSING
refreshes an already pending channel but does not itself open a popup. A matching
CBOR/error response or channel reinitialization ends that channel. Unrelated
channels, ping traffic, continuation packets, malformed lengths, and broadcast
channel IDs do not dismiss it. Three seconds without a relevant keepalive expires
stale state; unplug/poll failure closes the device's pending state.

One worker per device displays a popup while any observed channel remains pending.
A latest-state mailbox prevents missed close events or unbounded queues. A 120ms
presentation delay avoids flashing already-completed operations. Dismiss hides
the worker until that pending episode ends; subsequent keepalives cannot reopen
it. A later independent episode gets a new worker. Popup failure does not affect
signing, and is diagnosed rather than causing a hardware action.

Device-only labels are intentional: HID responses do not reliably identify an
application, SSH key, or server. This also covers browser WebAuthn requests and
forwarded-agent operations without special integration. Popups do not request
activation or reuse desktop activation tokens. X11 uses the EWMH zero user-time
hint; Wayland mapping focus is ultimately compositor policy. The separate app ID
`io.github.gtkaskpass_yubikey.touch` permits compositor-specific rules.

OpenSSH `none` touch helpers still retain their SIGTERM/parent-death lifetime,
but their duplicate windows are suppressed when passive monitoring covers the
connected interfaces and can display popups. Generic informational notifications
are unchanged. Without usable coverage/session routing, the existing OpenSSH UI
path remains enabled. `touchNotifications = false` disables passive monitoring.

These notifications report a device presence request, not successful SSH or
WebAuthn authentication. Completion, failure, cancellation, and observation timeout
all close a popup. The monitor currently interprets FIDO2 USB keepalives, not
legacy U2F polling or NFC/Bluetooth transports. The adapter is never kept in a
PIN request awaiting touch; signing can proceed as soon as it returns the PIN.

## Tracing, packaging, and verification

All diagnostics are stderr-only. `GTKASKPASS_TRACE=metadata` logs adapter input,
service/UI lifecycle, cache decisions, delivery, and exit without credentials.
`secrets` explicitly includes the final adapter response, escaped with its LF.
Workers and the service never enable secret traces. Native GTK informational
logging is suppressed; warnings/errors remain. Broken stderr must not affect
the authentication result.

Nix builds all three executables, wraps only the GTK worker, and supplies GTK4,
CGO, libfido2, and runtime dependencies. The NixOS module installs the commands
and graphical-session user service even with TTL zero. It exposes `cacheTTL`,
`pinVerification`, and `touchNotifications`. Systemd manages the private runtime
directory and its cleanup on restart; the daemon binds its own request socket.
Upgrade adapter/service/worker together; restarts clear caches.
The service runs as the user and relies on normal device ACLs rather than root.

Protocol/controller/cache/device interfaces and preference storage are independent
of GTK. Unit/race tests cover frame limits, stdout, generation races, wrong PINs,
retry states, device replacement, cancellation, and persistence. Fake hardware
backends test wrong PINs without consuming a user's physical retry budget.

GUI integration drives the real adapter/service/worker under private D-Bus, Xvfb,
and Openbox. Worker tests exercise chooser confirmation, cleared PIN retries, and
private IPC. Real OpenSSH/agent/forwarding tests use disposable software FIDO
keys in explicit unverified compatibility mode; they test SSH compatibility, not
physical verification. Weston exercises native Wayland lifecycle; a NixOS VM
tests graphical-session startup/shutdown, cache service configuration, and actual
kernel UHID report observation with a disposable virtual device. The fixture
also asserts that monitoring sends no output/feature reports. Pure-Go parser,
fuzz, channel/timeout, popup lifecycle, and X11 no-focus tests cover monitoring.
Physical verification is
user-assisted with correct PINs only; do not automate wrong attempts on real keys.

Run golangci-lint including integration code, formatting, vet, unit/race tests,
and `nix flake check`. CI builds on standard public x86-64 and ARM runners, with
the VM check on x86-64. Release permissions are limited to publishing jobs;
SHA-pinned Actions and Dependabot are maintained. See `docs/RELEASING.md`.

## References

- OpenSSH portable `readpass.c`, `ssh-agent.c`, and `sshconnect2.c`: askpass,
  notification termination, PIN retry, and signing behavior.
- libfido2 1.17.0 `src/pin.c` and `fido_dev_get_puat(3)`: fresh token acquisition
  and CTAP2.0/2.1 selection.
- libfido2 `fido_dev_get_retry_count(3)`: PIN retries, distinct from UV retries.
- gotk4 v0.4.1: GTK4/GLib bindings, thread and log integration.
