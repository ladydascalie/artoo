# AGENTS.md - Artoo Codebase Guide

## Project Overview

Artoo is a Cloudflare R2 bucket browser desktop application built with:
- **Backend**: Go 1.24+ with Wails v2 framework
- **Frontend**: Svelte 3 + Vite 3 (single-page app)
- **Purpose**: Browse, upload, download, and manage R2 bucket objects

## Project Structure

```
/
├── main.go           # Wails entry point, app lifecycle
├── client.go         # S3 client setup, App struct definition
├── config.go         # Configuration persistence (~/.config/artoo/)
├── objects.go        # Bucket/object listing, delete, search
├── upload.go         # Upload functionality with progress
├── download.go       # Download functionality with parallelism
├── preview.go        # Object preview (images, text, JSON, CSV)
├── stats.go          # Bucket statistics (GraphQL API or scan)
├── mise.toml         # Task runner and tool versions
├── wails.json        # Wails project configuration
└── frontend/
    ├── src/
    │   ├── App.svelte    # Main UI component (~1400 lines)
    │   ├── main.js       # Svelte entry point
    │   └── style.css     # Global styles
    └── wailsjs/          # Auto-generated Go bindings (do not edit)
```

## Build Commands

Use `mise` as the task runner:

| Command | Description |
|---------|-------------|
| `mise run dev` | Hot-reload development server |
| `mise run build` | Build production binary |
| `mise run install` | Install to system (Linux) |
| `mise run icons` | Regenerate PNG icons from SVG |

Frontend-only (from `frontend/` directory):

| Command | Description |
|---------|-------------|
| `npm run dev` | Vite dev server (standalone) |
| `npm run build` | Vite production build |

## Testing

**No test suite exists.** When adding tests:
- Go: Use standard `go test` with `_test.go` files
- Run single Go test: `go test -run TestFunctionName -v`
- Frontend: Would use Vitest if added

## Linting

**No linting configured.** If adding:
- Go: Use `go vet` and `staticcheck`
- Frontend: Consider Biome or ESLint

---

## Go Code Style

### Package Structure
- Single `main` package at repository root
- Files organized by concern: `client.go`, `objects.go`, `upload.go`, etc.
- All exported functions are exposed to frontend via Wails

### Naming Conventions

| Context | Convention | Example |
|---------|------------|---------|
| Exported functions/types | PascalCase | `ListBuckets`, `BucketInfo` |
| Unexported identifiers | camelCase | `configPath`, `buildClient` |
| JSON struct tags | snake_case | `json:"account_id"` |
| Method receivers | Single letter | `(a *App)` |

### Error Handling

Always wrap errors with context:

```go
if err != nil {
    return fmt.Errorf("failed to list objects: %w", err)
}
```

Errors returned from exported functions surface to the frontend automatically via Wails.

### Context and Timeouts

All S3 operations must use timeouts:

```go
ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
defer cancel()
```

### Progress Events

Use `a.emit()` to send progress messages to the frontend log panel:

```go
a.emit(fmt.Sprintf("Downloaded %s", key))
```

### Concurrency Pattern

Use `sync.WaitGroup` with semaphore channel for parallel operations:

```go
sem := make(chan struct{}, parallelism)
var wg sync.WaitGroup
for _, item := range items {
    wg.Add(1)
    sem <- struct{}{}
    go func(item Item) {
        defer wg.Done()
        defer func() { <-sem }()
        // work
    }(item)
}
wg.Wait()
```

### AWS SDK Patterns

Use `aws.String()` for SDK string pointers:

```go
input := &s3.GetObjectInput{
    Bucket: aws.String(bucket),
    Key:    aws.String(key),
}
```

---

## Frontend Code Style (Svelte/JavaScript)

### File Organization
- All UI lives in `App.svelte` (monolithic single-component architecture)
- Global styles in `style.css`
- Wails bindings auto-generated in `wailsjs/` (do not edit manually)

### Import Conventions

```javascript
// Svelte imports first
import { onMount, tick } from "svelte";

// Wails Go bindings
import { ListBuckets, DownloadFile } from "../wailsjs/go/main/App.js";

// Wails runtime
import { EventsOn } from "../wailsjs/runtime/runtime.js";
```

### State Management

Use Svelte reactive variables:

```javascript
let currentBucket = "";
let objects = [];
let loading = false;

// Reactive statements
$: filteredObjects = objects.filter(o => o.name.includes(search));
```

### Async/Error Handling

```javascript
async function loadObjects() {
    loading = true;
    error = null;
    try {
        objects = await ListObjects(currentBucket, prefix);
    } catch (e) {
        error = String(e);
    } finally {
        loading = false;
    }
}
```

### Event Listeners

Subscribe to backend events in `onMount`:

```javascript
onMount(() => {
    const unsubscribe = EventsOn("log", (msg) => {
        logs = [...logs, msg];
    });
    return unsubscribe;
});
```

### CSS Conventions

- Use CSS custom properties (variables) defined in `style.css`
- Scoped styles in `<style>` blocks within components
- Variable naming: `--kebab-case` (e.g., `--text-muted`, `--surface-hover`)

---

## Type Information

### Go Structs to Frontend

Structs with JSON tags are serialized to frontend:

```go
type ObjectInfo struct {
    Key          string    `json:"key"`
    Size         int64     `json:"size"`
    LastModified time.Time `json:"last_modified"`
}
```

Frontend receives these as plain JavaScript objects. TypeScript definitions are auto-generated in `frontend/wailsjs/go/models.ts`.

### No TypeScript in Frontend

Frontend uses plain JavaScript with `jsconfig.json` providing light type checking via `checkJs: true`.

---

## Configuration

User configuration stored at `~/.config/artoo/config.json`:

```json
{
    "account_id": "...",
    "access_key_id": "...",
    "secret_access_key": "...",
    "default_bucket": "..."
}
```

Access via `LoadConfig()` and `SaveConfig()` methods on `App`.

---

## Key Patterns to Follow

1. **Minimal dependencies**: Prefer standard library over external packages
2. **Error context**: Always wrap errors with descriptive messages
3. **Timeout all I/O**: Use `context.WithTimeout` for network operations
4. **Progress feedback**: Emit events for long-running operations
5. **Single responsibility**: Each Go file handles one domain concern
