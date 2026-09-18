{ lib, buildGoModule, pkg-config, gtk4, gobject-introspection, wrapGAppsHook4, adwaita-icon-theme }:
buildGoModule {
  pname = "gtkaskpass-yubikey";
  version = "0.1.0";
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../cmd ../internal ../tests/integration ../packaging
      ../go.mod ../go.sum ../LICENSE
    ];
  };
  vendorHash = "sha256-AT/GyatVsm9X3XuEV++WNhkV0EsmevE31NGVweMTBXA=";
  subPackages = [ "cmd/gtkaskpass-yubikey" "cmd/gtkaskpass-yubikey-cache" ];
  nativeBuildInputs = [ pkg-config gobject-introspection wrapGAppsHook4 ];
  buildInputs = [ gtk4 gobject-introspection adwaita-icon-theme ];
  env.CGO_ENABLED = 1;
  ldflags = [ "-s" "-w" ];
  dontWrapGApps = true;
  checkPhase = ''
    runHook preCheck
    test -z "$(gofmt -l cmd internal tests)"
    go vet ./...
    go test ./...
    go test -race ./internal/app ./internal/askpass ./internal/cache ./internal/cacheipc ./internal/lifecycle ./internal/trace
    runHook postCheck
  '';
  postInstall = ''
    install -Dm644 LICENSE "$out/share/licenses/gtkaskpass-yubikey/LICENSE"
    install -Dm644 packaging/gtkaskpass-yubikey-cache.socket "$out/lib/systemd/user/gtkaskpass-yubikey-cache.socket"
    substitute packaging/gtkaskpass-yubikey-cache.service "$out/lib/systemd/user/gtkaskpass-yubikey-cache.service" \
      --replace-fail '@bindir@' "$out/bin"
  '';
  postFixup = ''
    wrapProgram "$out/bin/gtkaskpass-yubikey" "''${gappsWrapperArgs[@]}"
  '';
  meta = {
    description = "GTK4 SSH askpass with touch notifications and an expiring in-memory credential cache";
    license = lib.licenses.asl20;
    platforms = lib.platforms.linux;
    mainProgram = "gtkaskpass-yubikey";
  };
}
