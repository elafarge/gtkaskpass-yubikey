{ self, lib, pkgs }:
{
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
  trace = lib.mkOption {
    type = lib.types.bool;
    default = false;
    description = "Enable metadata-only service diagnostics; credentials are never traced by the service.";
  };
}
