{
  description = "GTK4 SSH askpass with an expiring in-memory credential cache";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in rec {
          gtkaskpass-yubikey = pkgs.callPackage ./nix/package.nix { };
          default = gtkaskpass-yubikey;
        });
      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/gtkaskpass-yubikey";
          meta.description = "GTK4 SSH askpass";
        };
      });
      nixosModules.default = import ./nix/module.nix { inherit self; };
      checks = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          package = self.packages.${system}.default;
        in {
          inherit package;
          integration = pkgs.runCommand "gtkaskpass-integration" {
            nativeBuildInputs = with pkgs; [ go dbus xvfb-run xdotool openbox openssh ];
          } ''
            cp -r ${pkgs.lib.fileset.toSource {
              root = ./.;
              fileset = pkgs.lib.fileset.unions [
                ./go.mod ./go.sum ./tests/integration
                ./scripts/integration.sh ./tests/session.conf
              ];
            }} source
            chmod -R u+w source
            cd source
            export HOME="$TMPDIR/home"
            mkdir -p "$HOME"
            export GOCACHE="$TMPDIR/go-cache" GOPROXY=off
            export ASKPASS_BIN=${package}/bin/gtkaskpass-yubikey
            export CACHE_BIN=${package}/bin/gtkaskpass-yubikey-cache
            export FONTCONFIG_FILE=${pkgs.makeFontsConf { fontDirectories = [ pkgs.dejavu_fonts ]; }}
            bash scripts/integration.sh
            touch "$out"
          '';
          wayland = pkgs.runCommand "gtkaskpass-wayland" {
            nativeBuildInputs = with pkgs; [ python3 dbus weston ];
          } ''
            export HOME="$TMPDIR/home"
            mkdir -p "$HOME"
            export ASKPASS_BIN=${package}/bin/gtkaskpass-yubikey
            export FONTCONFIG_FILE=${pkgs.makeFontsConf { fontDirectories = [ pkgs.dejavu_fonts ]; }}
            dbus-run-session --config-file=${./tests/session.conf} -- python ${./tests/wayland.py}
            touch "$out"
          '';
          nixos = import ./nix/nixos-test.nix { inherit pkgs self package; };
        });
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
