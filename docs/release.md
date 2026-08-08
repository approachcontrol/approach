# Release Process

This project releases with GoReleaser. A pushed semver tag creates GitHub
Release artifacts with checksums, generated release notes, and a Homebrew cask.

## Homebrew Tap Setup

Homebrew casks are published to `approachcontrol/homebrew-tap`. Homebrew exposes
that repository as the short tap name `approachcontrol/tap`, so users install approach
with:

```bash
brew install --cask approachcontrol/tap/approach
```

GoReleaser commits the generated cask to `Casks/approach.rb` in the tap repository.
The release workflow needs a repository secret named
`HOMEBREW_TAP_GITHUB_TOKEN` because the default `GITHUB_TOKEN` cannot write to
another repository.

Create a GitHub personal access token with contents write access to
`approachcontrol/homebrew-tap`, then add it to this repository as:

```text
HOMEBREW_TAP_GITHUB_TOKEN
```

## Cutting a Release

1. Land the release changes on `main`.
2. Wait for CI and the `Release Snapshot` workflow to pass.
3. Optional local check if GoReleaser is installed:

   ```bash
   goreleaser release --snapshot --clean --skip=publish
   ```

4. Create and push the annotated tag (replace `vX.Y.Z` with the next semver
   tag):

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

5. Watch the `Release` workflow for the tag.

## Release Verification Checklist

After the `Release` workflow finishes, confirm the following (substitute the
release version for `X.Y.Z`):

1. The GitHub Release has these artifacts:
   - `approach_X.Y.Z_darwin_amd64.tar.gz`
   - `approach_X.Y.Z_darwin_arm64.tar.gz`
   - `approach_X.Y.Z_linux_amd64.tar.gz`
   - `approach_X.Y.Z_linux_arm64.tar.gz`
   - `approach_X.Y.Z_checksums.txt`
2. The release notes were generated from commits since the previous tag.
3. The cask was committed to `Casks/approach.rb` in `approachcontrol/homebrew-tap`, and
   the Homebrew install works:

   ```bash
   brew install --cask approachcontrol/tap/approach
   approach --version
   ```

4. The Go install fallback works:

   ```bash
   go install github.com/approachcontrol/approach/cmd/approach@vX.Y.Z
   approach --version
   ```
