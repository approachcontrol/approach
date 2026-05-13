# Release Process

This project releases with GoReleaser. A pushed semver tag creates GitHub
Release artifacts with checksums and generated release notes.

## First Release

The first release tag is `v0.1.0`.

1. Confirm the latest `main` build is green.
2. Confirm `.github/workflows/ci.yml` has passed the `release-snapshot` job.
3. Optional local check if GoReleaser is installed:

   ```bash
   goreleaser release --snapshot --clean --skip=publish
   ```

4. Create and push the annotated tag:

   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

5. Watch the `Release` workflow for the tag.
6. Verify the GitHub Release has these artifacts:
   - `wtui_0.1.0_darwin_amd64.tar.gz`
   - `wtui_0.1.0_darwin_arm64.tar.gz`
   - `wtui_0.1.0_linux_amd64.tar.gz`
   - `wtui_0.1.0_linux_arm64.tar.gz`
   - `wtui_0.1.0_checksums.txt`
7. Verify the release notes were generated from commits since the previous tag.
8. Verify the Go install fallback:

    ```bash
    go install github.com/brian-bell/wtui/cmd/wtui@v0.1.0
    wtui --version
    ```

## Later Releases

1. Land the release changes on `main`.
2. Wait for CI, including `release-snapshot`, to pass.
3. Tag the release with the next semver tag:

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. Verify GitHub Release artifacts, generated release notes, and `wtui --version`.
