
// bdges

```bash
# Regenerating the module sums
go mod tidy
# Build
go build ./cmd/openclaw
# Run the interactive setup
go run ./cmd/openclaw init --output openclaw.yaml
# Run tests
go test ./... -v
```

## Release Flow

- `publish` is handled by `.github/workflows/publish.yml`
- `release` is handled by `.github/workflows/release.yml`
- pushing a `v*` tag creates a draft release with `openclaw_<version>_darwin_arm64` and `openclaw_<version>_linux_amd64`
- the release workflow promotes the draft after running smoke tests
