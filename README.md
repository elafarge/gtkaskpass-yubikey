# ssh-askpass-fido

[![CI](https://github.com/elafarge/ssh-askpass-fido/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/elafarge/ssh-askpass-fido/actions/workflows/ci.yml)

A Linux **ssh-askpass alternative for people using FIDO2 security keys with SSH**.
Less repetitive PIN typing, clearer prompts, and a reminder when your key actually
needs a touch. Works with compatible FIDO2 devices, not just YubiKeys.

## Features

- **PIN caching:** enter your device PIN once, reuse it for a configurable duration
  (one hour by default), including forwarded-agent operations such as remote Git.
- **PIN verification:** reject incorrect PINs before returning them to SSH and
  show remaining attempts when the device supports it.
- **Timely touch reminders:** popups follow actual USB FIDO2 presence requests,
  including browser use, rather than guessing from a timer. Hardware touch is
  still required; dismissing a popup doesn't approve or cancel an operation.
- **Device selection:** automatically use one connected device, or choose among
  several; remember preferences when reliable device identifiers are available.
- **Normal askpass support:** passphrases, other input prompts, confirmations,
  notifications, and cancellation.
- **Lightweight Go service and GTK4 dialogs**, supporting Wayland and X11.
  Credentials stay in memory; only public device preferences persist.

## Install and configure (NixOS)

Add the flake input:

```nix
inputs.ssh-askpass-fido.url = "github:elafarge/ssh-askpass-fido";
```

Then import its module (`inputs` must be supplied through NixOS `specialArgs`):

```nix
{ inputs, ... }: {
  imports = [ inputs.ssh-askpass-fido.nixosModules.default ];
  services.ssh-askpass-fido = {
    enable = true;
    cacheTTL = "1h";             # "0" disables caching
    pinVerification = "required";
    touchNotifications = true;
  };
}
```

Rebuild NixOS. The service runs with your graphical session. Configure your SSH
agent to inherit `SSH_ASKPASS` and the desktop environment; for terminal input,
set `SSH_ASKPASS_REQUIRE=prefer` if you want GUI prompts. Restart existing agents
after changing their environment and reload their keys as needed.

To forget cached credentials:

```sh
ssh-askpass-fido-service forget --all
```

[Configuration reference](docs/CONFIGURATION.md) ·
[Upgrade/rename guide](docs/MIGRATION.md) ·
[Troubleshooting](docs/USAGE.md)

NixOS is the supported packaging path. Contributions for other Linux distributions
and package managers are welcome.

## Floating windows

These rules cover both interactive dialogs and passive touch reminders.

**Hyprland (Lua config):**

```lua
hl.window_rule({ name = "ssh-askpass-fido", float = true,
  match = { class = "^io[.]github[.]ssh_askpass_fido([.]touch)?$" } })
```

**Sway:**

```sway
for_window [app_id="^io[.]github[.]ssh_askpass_fido([.]touch)?$"] floating enable
```

**Niri:**

```kdl
window-rule {
    match app-id=r#"^io[.]github[.]ssh_askpass_fido([.]touch)?$"#
    open-floating true
}
```

## About

Built entirely with **OpenCode, powered by OpenAI GPT-6 Astra**, from requirements,
feedback, and hardware testing provided by Étienne Lafarge.

[Design](docs/DESIGN.md) · [Tests](tests/README.md) · [Contributing notes](AGENTS.md)

Copyright © 2026 Étienne Lafarge. Licensed under [Apache-2.0](LICENSE).
