{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.gtkaskpass-yubikey;
  enabledCache = cfg.cacheTTL != "0";
  executable = lib.getExe cfg.package;
  noCache = pkgs.writeShellScript "gtkaskpass-yubikey-no-cache" ''
    export GTKASKPASS_CACHE=off
    exec ${executable} "$@"
  '';
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
  };
  config = lib.mkIf cfg.enable {
    programs.ssh.enableAskPassword = true;
    programs.ssh.askPassword = if enabledCache then executable else toString noCache;
    environment.systemPackages = [ cfg.package ];
    systemd.user.sockets.gtkaskpass-yubikey-cache = lib.mkIf enabledCache {
      description = "SSH askpass credential cache socket";
      wantedBy = [ "sockets.target" ];
      socketConfig = {
        ListenStream = "%t/gtkaskpass-yubikey/cache.sock";
        SocketMode = "0600";
        DirectoryMode = "0700";
        RemoveOnStop = true;
      };
    };
    systemd.user.services.gtkaskpass-yubikey-cache = lib.mkIf enabledCache {
      description = "SSH askpass in-memory credential cache";
      requires = [ "gtkaskpass-yubikey-cache.socket" ];
      after = [ "gtkaskpass-yubikey-cache.socket" ];
      serviceConfig = {
        ExecStart = "${cfg.package}/bin/gtkaskpass-yubikey-cache serve --ttl ${cfg.cacheTTL}";
        Restart = "on-failure";
        NoNewPrivileges = true;
        LimitCORE = 0;
        UMask = "0077";
      };
    };
  };
}
