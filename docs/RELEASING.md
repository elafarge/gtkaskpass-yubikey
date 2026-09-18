# CI and releases

## Cost and service choice

Checked against GitHub's documentation on 2026-09-18:

- [Standard GitHub-hosted Actions runners are free and unlimited for public
  repositories](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#standard-github-hosted-runners-for-public-repositories).
  CI uses `ubuntu-24.04` and `ubuntu-24.04-arm`, not paid larger runners.
- [Public GitHub Packages are free](https://docs.github.com/en/billing/concepts/product-billing/github-packages).
  However, [its supported registries](https://docs.github.com/en/packages/learn-github-packages/introduction-to-github-packages#supported-clients-and-formats)
  do not include Go modules or Nix packages. GHCR supports containers/OCI, which
  is not required to install this GTK desktop application.
- [GitHub Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases#storage-and-bandwidth-quotas)
  support downloadable files without a total release-size or bandwidth limit;
  each individual asset must be smaller than 2 GiB. This is our binary
  distribution channel. NixOS users can also consume the repository's flake.
- Workflows do not use metered Actions artifacts or persistent caches. Release
  jobs upload directly to a draft release. This trades a repeated build on release
  tags for avoiding artifact-storage charges or a separate cache service.

No paid runner, external signing/cache service, or personal access token is needed.
GitHub's published terms can change; the linked billing documentation is the
authority, and account-wide settings/usage from other repositories are separate.

## Service/worker compatibility

Version 0.2.0 packages the thin adapter, headless request/FIDO service, and GTK
worker together. Deploy all three from the same package and restart the service;
the adapter uses request protocol v2. TTL zero still requires the service.
Required FIDO PIN verification is the default; compatibility providers must opt
into `pinVerification = "off"`. Runtime closures now include libfido2 as well as
GTK. Public device preferences persist independently of the in-memory cache.

Version 0.3.0 replaces socket activation with graphical-session startup and adds
read-only USB FIDO touch monitoring. The old `.socket` unit is removed; stop and
disable it when upgrading a manual installation, then enable/start the `.service`
after importing the graphical environment. NixOS owns that transition through
the module. The daemon still serves its private socket, but never relies on a
client connection to start monitoring. Cache entries are cleared on restart.

## CI

`CI` runs on pushes to `main`, pull requests, and manual dispatch. The reusable
`Checks` workflow builds natively on both supported architectures and runs:

- golangci-lint (including integration-tagged Go files), formatting, vet,
  unit tests, and pure-Go race tests in the Nix package check phase;
- X11 GTK and real OpenSSH integration tests;
- native Wayland smoke tests;
- the NixOS VM test on x86-64, with a real readable/writable `/dev/kvm`;
- workflow/shell lint and release-verifier unit tests on x86-64.

ARM runners execute the native package and GUI checks; nested virtualization is
not assumed there. Missing KVM on the x86-64 job is a failure, not a skipped pass.
Actions are pinned to commit SHAs. Dependabot updates Actions and Go dependencies.
Go dependency updates may also require updating `vendorHash` in `nix/package.nix`.
Update `flake.lock` deliberately and verify the GTK/Go/native-library combination.

Pull requests use read-only permissions and `pull_request`, never
`pull_request_target`. Checkout does not persist credentials. Only release
publishing jobs receive `contents: write`.

## Publish a version

1. Update `version` in `nix/package.nix`, document changes, and run the checks.
2. Commit and push the change to `main`; wait for CI to pass.
3. Create and push a stable version tag matching the package version:

   ```sh
   git tag -a v0.1.0 -m "Release 0.1.0"
   git push origin v0.1.0
   ```

The `Release` workflow reruns both architectures' checks and rejects tags that
do not match `nix/package.nix`. After every check passes, it creates a draft,
builds a runtime closure on each native architecture, and uploads:

```text
ssh-askpass-fido-VERSION-x86_64-linux.nar.zst
ssh-askpass-fido-VERSION-x86_64-linux.json
ssh-askpass-fido-VERSION-x86_64-linux.sha256
ssh-askpass-fido-VERSION-aarch64-linux.nar.zst
ssh-askpass-fido-VERSION-aarch64-linux.json
ssh-askpass-fido-VERSION-aarch64-linux.sha256
```

The NAR stream contains the package and its runtime dependencies, including GTK.
Metadata records the version, native system, source commit, and package store path.
The final job verifies both architecture manifests, checksums, upload completion,
and GitHub's server-side asset SHA-256 digests before publishing the draft.

A failed job leaves the draft unpublished. Fix transient failures and rerun;
assets may be replaced only while the release is still a draft. Published
releases are not overwritten. Publish a new version for corrected software.

Repository creation and CI setup do not themselves create a version tag or an
initial release. Tagging a version is the release trigger.

## Install a release bundle

The usual NixOS module/flake remains the simplest installation. Prebuilt bundles
are useful when you want to import the exact published package without compiling.
They require Nix and `zstd`; they are not standalone binaries for arbitrary GTK
versions.

Download the three assets for your architecture from the GitHub release, then:

```sh
sha256sum --check ssh-askpass-fido-0.4.0-x86_64-linux.sha256
zstd -dc ssh-askpass-fido-0.4.0-x86_64-linux.nar.zst | sudo nix-store --import
package=$(jq -r .storePath ssh-askpass-fido-0.4.0-x86_64-linux.json)
nix profile add "$package"
```

The exported store paths are not signed as a Nix binary cache, so a multi-user
installation may need an administrator/trusted user for import. SHA-256 verifies
the downloaded files against the release; obtain the checksum from the intended
repository/release. Importing a closure does not configure your SSH environment
or enable the user service; follow `README.md` for that setup.

## Test release tooling locally

```sh
nix develop --command actionlint
nix develop --command shellcheck scripts/*.sh
nix develop --command python3 -m unittest discover -s tests/release
nix develop --command bash scripts/release.sh check-tag v0.1.0 x86_64-linux
nix develop --command bash scripts/release.sh bundle v0.1.0 x86_64-linux dist
```

The local bundle command requires a clean committed source tree, builds and checks the native package, compresses its
runtime closure, tests the compressed stream, and verifies the generated
checksums. It performs no GitHub publication.
