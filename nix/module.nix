{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.gtkaskpass-yubikey;
  executable = lib.getExe cfg.package;
in {
  options.services.gtkaskpass-yubikey = {
    enable = lib.mkEnableOption "GTK4 SSH askpass and its per-user credential cache";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      description = "Package containing the askpass frontend and cache daemon.";
    };
    cacheTTL = lib.mkOption {
      type = lib.types.strMatching "0|([0-9]+(\\.[0-9]+)?(ns|us|ms|s|m|h))+";
      default = "1h";
      example = "15m";
      description = "Absolute credential lifetime in Go duration syntax; 0 disables caching.";
    };
    pinVerification = lib.mkOption {
      type = lib.types.enum [ "required" "off" ];
      default = "required";
      description = "Verify FIDO PINs against the selected device; off is unverified compatibility mode.";
    };
    touchNotifications = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Passively monitor USB FIDO2 user-presence requests and show device-level popups in the service's graphical session.";
    };
  };
  config = lib.mkIf cfg.enable {
    programs.ssh.enableAskPassword = true;
    programs.ssh.askPassword = executable;
    environment.systemPackages = [ cfg.package ];
    systemd.user.services.gtkaskpass-yubikey-cache = {
      description = "SSH askpass service and passive FIDO2 touch monitor";
      wantedBy = [ "graphical-session.target" ];
      partOf = [ "graphical-session.target" ];
      after = [ "graphical-session-pre.target" ];
      serviceConfig = {
        ExecStart = "${cfg.package}/bin/gtkaskpass-yubikey-cache serve --ttl ${cfg.cacheTTL} --pin-verification ${cfg.pinVerification}" + lib.optionalString cfg.touchNotifications " --touch-monitor";
        Restart = "on-failure";
        RuntimeDirectory = "gtkaskpass-yubikey";
        RuntimeDirectoryMode = "0700";
        NoNewPrivileges = true;
        LimitCORE = 0;
        UMask = "0077";
      };
    };
  };
}
