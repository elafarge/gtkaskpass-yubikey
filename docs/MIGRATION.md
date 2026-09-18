# Renaming to ssh-askpass-fido

The project was previously named `gtkaskpass-yubikey`. The new name reflects its
SSH/FIDO role rather than its UI toolkit or one authenticator vendor.

| Previous | Current |
| --- | --- |
| `elafarge/gtkaskpass-yubikey` | `elafarge/ssh-askpass-fido` |
| `gtkaskpass-yubikey` executable | `ssh-askpass-fido` |
| `gtkaskpass-yubikey-cache` executable | `ssh-askpass-fido-service` |
| `gtkaskpass-yubikey-ui` executable | `ssh-askpass-fido-ui` |
| `gtkaskpass-yubikey-cache.service` | `ssh-askpass-fido.service` |
| `services.gtkaskpass-yubikey` NixOS option | `services.ssh-askpass-fido` |
| `GTKASKPASS_TRACE` / `GTKASKPASS_CACHE` | `SSH_ASKPASS_FIDO_TRACE` / `SSH_ASKPASS_FIDO_CACHE` |
| `~/.config/gtkaskpass-yubikey/` | `~/.config/ssh-askpass-fido/` |
| `$XDG_RUNTIME_DIR/gtkaskpass-yubikey/` | `$XDG_RUNTIME_DIR/ssh-askpass-fido/` |
| `io.github.gtkaskpass_yubikey[.touch]` | `io.github.ssh_askpass_fido[.touch]` |

Update the flake input URL/name, module option, `SSH_ASKPASS`, agent environment,
and any compositor rules. GitHub redirects the previous repository URL, but
configuration should use the new URL explicitly. Release assets and the Go
module path use the new name. The design document is now `docs/DESIGN.md`.

Stop the old service before starting the new one to avoid duplicate device
monitors. NixOS removes its managed old unit when rebuilding the updated module;
manually installed user units should also be disabled and removed. Rebuild both
NixOS and Home Manager when each manages a part of the configuration.

With both services stopped, move an existing user config or `devices.json` into
the new config directory, preserving private permissions. Do not overwrite files
already present there without comparing them first. The application deliberately
does not read both old and new directories or silently merge policies. No secret
file needs migration: in-memory credentials are cleared by the restart.

Reload the keys in an SSH agent if updating its environment restarted it. Close
old shells or update their exported `SSH_ASKPASS` and trace/cache variable names.
