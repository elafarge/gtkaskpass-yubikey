{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.ssh-askpass-fido;
  executable = lib.getExe cfg.package;
  configFile = (pkgs.formats.json { }).generate "ssh-askpass-fido-config.json" {
    inherit (cfg) cacheTTL pinVerification touchNotifications trace;
  };
in {
  options.services.ssh-askpass-fido = import ./options.nix { inherit self lib pkgs; };
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = pkgs.stdenv.hostPlatform.isLinux;
        message = "services.ssh-askpass-fido requires Linux.";
      }
    ];
    home.packages = [ cfg.package ];
    home.sessionVariables.SSH_ASKPASS = executable;
    systemd.user.sessionVariables.SSH_ASKPASS = executable;
    systemd.user.services.ssh-askpass-fido = {
      Unit = {
        Description = "SSH askpass service and passive FIDO2 touch monitor";
        PartOf = [ "graphical-session.target" ];
        After = [ "graphical-session-pre.target" ];
      };
      Install.WantedBy = [ "graphical-session.target" ];
      Service = {
        ExecStart = "${cfg.package}/bin/ssh-askpass-fido-service serve --config ${configFile}";
        Restart = "on-failure";
        RuntimeDirectory = "ssh-askpass-fido";
        RuntimeDirectoryMode = "0700";
        NoNewPrivileges = true;
        LimitCORE = 0;
        UMask = "0077";
      };
    };
  };
}
