{ lib, buildGoModule, pkg-config, gtk4, gobject-introspection, wrapGAppsHook4, adwaita-icon-theme, golangci-lint, libfido2 }:
buildGoModule {
  pname = "gtkaskpass-yubikey";
  version = "0.4.0";
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../cmd ../internal ../tests/integration ../packaging
      ../go.mod ../go.sum ../LICENSE ../.golangci.yml
    ];
  };
  vendorHash = "sha256-Rghyi4e5RgUQJNzlKWYIC4zh0rJr9SZnJrFMq+zfT3A=";
  subPackages = [ "cmd/gtkaskpass-yubikey" "cmd/gtkaskpass-yubikey-cache" "cmd/gtkaskpass-yubikey-ui" ];
  nativeBuildInputs = [ pkg-config gobject-introspection wrapGAppsHook4 golangci-lint ];
  buildInputs = [ gtk4 gobject-introspection adwaita-icon-theme libfido2 ];
  env.CGO_ENABLED = 1;
  ldflags = [ "-s" "-w" ];
  dontWrapGApps = true;
  checkPhase = ''
    runHook preCheck
    test -z "$(gofmt -l cmd internal tests)"
    export GOLANGCI_LINT_CACHE="$TMPDIR/golangci-lint"
    golangci-lint run
    go vet ./...
    go test ./...
    go test -race ./internal/app ./internal/askpass ./internal/cache ./internal/cacheipc ./internal/lifecycle ./internal/trace ./internal/preferences ./internal/service ./internal/touch ./internal/config
    runHook postCheck
  '';
  postInstall = ''
    install -Dm644 LICENSE "$out/share/licenses/gtkaskpass-yubikey/LICENSE"
    install -Dm644 packaging/config.yaml "$out/share/gtkaskpass-yubikey/config.example.yaml"
    mkdir -p "$out/lib/systemd/user"
    substitute packaging/gtkaskpass-yubikey-cache.service "$out/lib/systemd/user/gtkaskpass-yubikey-cache.service" \
      --replace-fail '@bindir@' "$out/bin"
  '';
  postFixup = ''
    wrapProgram "$out/bin/gtkaskpass-yubikey-ui" "''${gappsWrapperArgs[@]}"
  '';
  meta = {
    description = "GTK4 SSH askpass with touch notifications and an expiring in-memory credential cache";
    homepage = "https://github.com/elafarge/gtkaskpass-yubikey";
    license = lib.licenses.asl20;
    platforms = lib.platforms.linux;
    mainProgram = "gtkaskpass-yubikey";
  };
}
