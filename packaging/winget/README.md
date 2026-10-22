# winget manifests

Three manifests describe the Windows desktop build:

| File | Purpose |
|---|---|
| `coddan.Sextant.yaml` | Version header |
| `coddan.Sextant.installer.yaml` | Installer URL, type and SHA256 |
| `coddan.Sextant.locale.en-US.yaml` | Store listing text |

The package installs `sextant-gui-x86_64-pc-windows-gnu.zip` from the GitHub
release as a **portable** package, so `sextant-gui` ends up on `PATH` without a
setup wizard.

The `InstallerSha256` in the committed manifest is a placeholder. It is replaced
with the real hash whenever a release is cut.

## Publishing a new version

The release workflow does the mechanical work:

1. Tag and push a release (`vX.Y.Z`). The publish job generates `SHA256SUMS`,
   runs `scripts/winget-update.sh` against it and uploads a filled
   `winget-manifests-vX.Y.Z.zip` next to the archives.
2. If a `WINGET_TOKEN` repository secret is configured (a GitHub token with
   `public_repo` access), the `winget` job validates the manifests with
   `wingetcreate` and opens the PR to [microsoft/winget-pkgs]. Without the
   secret that job only prints a notice.
3. To submit manually, download `winget-manifests-vX.Y.Z.zip` from the release,
   unzip it and run:

   ```sh
   wingetcreate validate manifests/coddan/Sextant/X.Y.Z
   wingetcreate submit manifests/coddan/Sextant/X.Y.Z
   ```

## Updating the manifests by hand

`scripts/winget-update.sh` fills the version, URL and hash. It takes the hash as
an argument, or reads it from a `SHA256SUMS` file on stdin:

```sh
curl -fsSL https://github.com/coddan/sextant/releases/download/vX.Y.Z/SHA256SUMS |
  scripts/winget-update.sh X.Y.Z

# or, from a local build:
scripts/winget-update.sh X.Y.Z "$(sha256sum sextant-gui-x86_64-pc-windows-gnu.zip | cut -d' ' -f1)"
```

Then validate:

```sh
winget validate packaging/winget
```

[microsoft/winget-pkgs]: https://github.com/microsoft/winget-pkgs
