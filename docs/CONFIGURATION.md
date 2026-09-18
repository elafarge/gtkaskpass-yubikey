# Service configuration

`ssh-askpass-fido-service` uses **Cobra** for commands/help/flags and **Koanf**
for configuration loading and precedence. The askpass adapter itself continues
to treat every argument as OpenSSH prompt data, including a prompt named `--help`.

## Location and formats

Without `--config`, the service looks for one of these files:

```text
$XDG_CONFIG_HOME/ssh-askpass-fido/config.yaml
$XDG_CONFIG_HOME/ssh-askpass-fido/config.yml
$XDG_CONFIG_HOME/ssh-askpass-fido/config.toml
$XDG_CONFIG_HOME/ssh-askpass-fido/config.json
```

`XDG_CONFIG_HOME` defaults to `~/.config`. No file is required. If multiple formats
exist, startup fails rather than silently choosing one. An explicit
`--config /path/to/config.yaml` selects only that file and fails if it is missing.
The extension determines the parser. There is no automatic system-wide config or
arbitrary environment-variable override; administrators can select an `/etc` file
in their service's `ExecStart`.

Configuration is read once at startup. After editing, run:

```sh
ssh-askpass-fido-service config check
systemctl --user restart ssh-askpass-fido.service
```

A restart clears in-memory credentials and closes outstanding requests. There is
no live reload that could change verification policy midway through a request.

## Schema

```yaml
cacheTTL: 1h
pinVerification: required
touchNotifications: true
trace: false
```

The equivalent TOML is:

```toml
cacheTTL = "1h"
pinVerification = "required"
touchNotifications = true
trace = false
```

Or JSON:

```json
{
  "cacheTTL": "1h",
  "pinVerification": "required",
  "touchNotifications": true,
  "trace": false
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `cacheTTL` | `"1h"` | Nonnegative Go duration: e.g. `15m`, `2h`, or `"0"` to disable credential retention |
| `pinVerification` | `"required"` | `required` verifies FIDO PINs; `off` explicitly enables unverified compatibility mode |
| `touchNotifications` | `true` | Passive USB FIDO touch monitoring in the service's graphical session |
| `trace` | `false` | Metadata-only service tracing; never credential payloads |

Keys are case-sensitive. Unknown keys, nulls, incorrect types, invalid durations,
and unknown verification modes are errors. An omitted key inherits its default.
Config files must be regular files, are limited to 64 KiB, and are never written
by the application. Symlinks are supported, including Nix-generated config files.

This file contains settings only. PINs/passphrases are never stored here.
`devices.json` remains a separate service-managed public device-preference file.

## CLI overrides

Precedence is **defaults → selected file → explicitly supplied flags**.
Unspecified flags never overwrite the file's values. Existing flag names remain:

```sh
ssh-askpass-fido-service serve --config ./config.yaml --ttl 15m
ssh-askpass-fido-service serve --touch-monitor=false --trace=false
ssh-askpass-fido-service config show --config ./config.toml --pin-verification required
```

| File key | Override flag |
| --- | --- |
| `cacheTTL` | `--ttl` |
| `pinVerification` | `--pin-verification` |
| `touchNotifications` | `--touch-monitor` |
| `trace` | `--trace` |

Use `--flag=false` to explicitly disable a boolean set in the file. `config check`
validates effective settings, while `config show` prints them as JSON. Neither
starts the service or accesses an authenticator. Both accept the same overrides
as `serve`. `devices` and `forget` do not load service configuration, so a bad
config file does not prevent forgetting credentials.

The standalone packaged unit executes only `ssh-askpass-fido-service serve`, so
it naturally uses the user's config file. Touch monitoring is enabled by default;
headless users and tests can disable it explicitly. An example YAML file is
installed under `share/ssh-askpass-fido/config.example.yaml`.

## NixOS

The existing module options remain the user-facing configuration:

```nix
services.ssh-askpass-fido = {
  enable = true;
  cacheTTL = "1h";
  pinVerification = "required";
  touchNotifications = true;
  trace = false;
};
```

The module generates a JSON file in the Nix store and starts the service with
`serve --config /nix/store/…-ssh-askpass-fido-config.json`. It no longer encodes
the settings as individual command-line flags. Because the file is explicitly
selected, a user's XDG config does not override the declarative NixOS policy.
Use the module options to change it; changes to the generated file's store path
cause the service definition to change on rebuild.

No secrets belong in Nix-generated configuration: the store is public to local
users. Credential and device-preference storage behavior is unchanged.
