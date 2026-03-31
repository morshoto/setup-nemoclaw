
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
