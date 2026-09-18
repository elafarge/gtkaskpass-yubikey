{
  description = "GTK4 SSH askpass with an expiring in-memory credential cache";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      devShells = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in {
          default = pkgs.mkShell {
            nativeBuildInputs = with pkgs; [
              go pkg-config gobject-introspection wrapGAppsHook4
              dbus xvfb-run xdotool openbox openssh python3 weston
            ];
            buildInputs = with pkgs; [ gtk4 ];
            shellHook = ''
              export GSETTINGS_SCHEMA_DIR=${pkgs.gtk4}/share/gsettings-schemas/${pkgs.gtk4.name}/glib-2.0/schemas
            '';
          };
        });
    };
}
