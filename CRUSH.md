# WuKongIM Development Guide

## Commands

### Go Development
- **Build**: `go build -o wukongim main.go`
- **Test**: `go test ./...` (all tests)
- **Single test**: `go test -run TestName ./path/to/package`
- **Test with verbose**: `go test -v ./...`
- **Race detection**: `go test -race ./...`

### Frontend (Vue.js)
- **Dev server**: `cd demo/chatdemo && npm run dev` or `cd web && npm run dev`
- **Build**: `npm run build` (vue-tsc && vite build)
- **Preview**: `npm run preview`

### Docker
- **Build**: `make build`
- **Deploy**: `make deploy` (production), `make deploy-dev` (development)

## Code Style

### Import Organization
```go
import (
    // Standard library (alphabetical)
    "context"
    "fmt"
    
    // Third-party (alphabetical) 
    "github.com/spf13/cobra"
    "go.uber.org/zap"
    
    // Internal (alphabetical by module)
    "github.com/WuKongIM/WuKongIM/internal/server"
    "github.com/WuKongIM/WuKongIM/pkg/wknet"
)
```

### Naming Conventions
- **Variables**: `camelCase` (local), `camelCase` (package-level)
- **Functions**: `CamelCase` (public), `camelCase` (private)
- **Structs**: `CamelCase`
- **Constants**: `UPPER_SNAKE_CASE` or `UpperCamelCase`
- **Packages**: lowercase, short (e.g., `wknet`, `wkutil`)

### Error Handling
```go
if err != nil {
    m.Error("description", zap.Error(err))
    return err
}
```
- Always check errors immediately
- Use structured logging with zap
- Early return pattern

### Code Structure
- File header: package, imports, globals, types, methods
- Embed `wklog.Log` in structs for consistent logging
- Use functional options pattern for configuration
- Follow standard `gofmt` formatting

### Testing
- Test files: `*_test.go`
- Use `testify` for assertions
- Performance tests: `*_benchmark_test.go`
- Integration tests: `*_integration_test.go`