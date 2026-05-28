# PostIt — The Local-First, All-in-One API Client

![Go Version](https://img.shields.io/badge/Go-1.25.5-blue)
![License](https://img.shields.io/badge/license-MIT-green)
![Platform](https://img.shields.io/badge/platform-Windows%20|%20macOS%20|%20Linux-lightgrey)

**PostIt** is a modern, local-first API client and developer tool built in Go. It is designed to be a powerful alternative to Postman, Insomnia, and Hoppscotch — with a focus on performance, developer experience, and extensibility. Whether you are testing a REST API, orchestrating a multi-step workflow, load-testing an endpoint, or querying a database side-by-side, PostIt does it all — completely offline, with no cloud dependency.

---

## Table of Contents

- [Quick Start](#quick-start)
- [User Interfaces](#user-interfaces)
- [Core Features](#core-features)
- [Power Tools](#power-tools)
- [Import & Export](#import--export)
- [Security & Vault](#security--vault)
- [Architecture](#architecture)
- [Building from Source](#building-from-source)
- [Configuration](#configuration)
- [Roadmap](#roadmap)

---

## Quick Start

```bash
# Start the Web UI (default)
./postit.exe

# Start with a specific UI
./postit.exe -ui web        # Web UI (default)
./postit.exe -ui tui        # Terminal UI
./postit.exe -ui cli        # Interactive CLI menu

# Enable the mock server
./postit.exe -ui web -mock

# Import Postman collections on startup
./postit.exe -import "collection1.json,collection2.json"

# Run a collection headlessly (CI/CD)
./postit.exe run --collection ./output/history.json --env ./output/environments.json --delay 500
```

---

## User Interfaces

PostIt supports four distinct interfaces, all sharing the same backend engine and storage:

| Interface | Flag | Description |
|-----------|------|-------------|
| **Web UI** | `-ui web` | Full-featured browser-based interface at `http://localhost:8080` |
| **Terminal UI** | `-ui tui` | Keyboard-driven TUI with split-pane editor (tview) |
| **Interactive CLI** | `-ui cli` | Survey-based terminal menu for headless/scripted usage |
| **Headless Runner** | `run` command | Execute collections without any UI (CI/CD pipelines) |

### Keyboard Shortcuts (TUI)

| Shortcut | Action |
|----------|--------|
| `Tab` | Cycle focus through panels |
| `Ctrl+P` | Command palette |
| `Ctrl+R` | Send request |
| `Ctrl+S` | Save request |
| `Ctrl+H` | Hammer (load test) |
| `Ctrl+Q` | SQL editor |
| `Ctrl+N` | New request |
| `Ctrl+D` | Duplicate request |
| `Ctrl+M` | Move request |
| `Alt+↑/↓` | Reorder requests in tree |

---

## Core Features

### Request Builder

Build and send HTTP requests with full control over:

- **Method**: GET, POST, PUT, DELETE, PATCH
- **URL**: Full URL with variable resolution
- **Headers**: Key-value pairs with autocomplete and bulk editing
- **Body Modes**:
  - `raw` — Plain text / JSON with syntax highlighting and beautify
  - `x-www-form-urlencoded` — Form field key-value pairs
  - `GraphQL` — Dedicated query + variables editor with schema introspection
- **Authentication**: Bearer token support built-in

### Variable System

PostIt resolves variables using `{{variable_name}}` syntax in URLs, headers, and body content. Resolution order:

1. **Local variables** (from Data-Driven Runner or Workflow context)
2. **Magic variables** (dynamic generators)
3. **Active environment** (O(1) cache lookup)
4. **Global variables** (shared across all requests)
5. **OS environment variables**
6. **Interactive prompt** (falls back to user input if no value found)

#### Magic Variables

| Variable | Description |
|----------|-------------|
| `{{$guid}}` | UUID v4 |
| `{{$timestamp}}` | Current Unix timestamp |
| `{{$isoTimestamp}}` | Current ISO-8601 timestamp |
| `{{$randomInt}}` | Random integer (0-1000) |
| `{{$randomPassword}}` | Secure 12-character random string |

### Script Engine

Write JavaScript-like scripts that run **before** (pre-request) and **after** (post-request/test) each request. The engine supports:

- Variable assignment and retrieval (`pm.collectionVariables.set/get`)
- JSON path extraction (`pm.response.json().data.users[0].id`)
- Response header access (`pm.response.headers.get('Content-Type')`)
- Request header access (`pm.request.headers.get('Authorization')`)
- Conditional logic (`if/else` with truthiness, `==`, `!=`, `&&`, `||`, `!`)
- Concatenation (`+`)
- Variable resolution inside scripts

### Environments

Manage multiple environments (e.g., Local, Staging, Production) with per-environment variables:

- Create, edit, and delete environments
- Switch active environment via the **ENV** dropdown in the toolbar
- **Secret variables** (marked with a lock icon) are encrypted with AES-GCM and stored safely
- Variables from the active environment are resolved automatically in all requests

### History

Every request sent is automatically recorded with full context:

- Timestamp, method, URL, status code, duration
- **Full response body and headers** saved (time-travel debugging)
- Click any history item to restore the exact response snapshot
- Export history as **JSON** or **CSV**
- Visualize request flow as a Mermaid.js sequence diagram

### Response Viewer

The response panel offers multiple analysis tabs:

- **Body** — Raw response with JSON syntax highlighting, expand/collapse, and copy support
- **Headers** — Response headers in a clean table view
- **Preview** — HTML rendered in an iframe or image preview
- **JWT** — Automatically detects, extracts, and decodes JSON Web Tokens
- **Diff** — Side-by-side comparison with a pinned baseline response
- **Security** — One-click fuzzer results (SQLi, XSS, robustness)

#### Advanced JSON Filter Engine

The filter bar in the response body supports a powerful query language:

| Feature | Example |
|---------|---------|
| Drill-down | `data.user.profile` |
| Deep search | `**.id` (finds all IDs at any level) |
| Exclude keys | `-data.logs` |
| Deep exclude | `-**.password` |
| Predicates | `data.users[id=5]`, `products[price>100]` |
| Operators | `=`, `!=`, `>`, `<`, `>=`, `<=` |
| Field projection | `data.users{id, username, email}` |
| Flatten | `data \| flatten` |
| Count | `data.items \| count` |
| First/Last | `data.items \| first`, `data.items \| last` |
| Sort | `data.users \| sort:name` |
| Keys | `data \| keys` |
| Multi-filter | `-**.debugInfo data.users[active=true]` |
| Chained pipes | `data.users[role=admin]{id, name} \| sort:name` |

---

## Power Tools

### Hammer (Load Testing)

Stress-test any endpoint with configurable concurrency:

- **Workers**: Number of concurrent goroutines (default: 10)
- **Duration**: Test duration in seconds (default: 5)
- **Streaming mode**: Real-time progress updates via Server-Sent Events (newline-delimited JSON)
- **Live Dashboard**: Total requests, RPS, average latency, P95/P99 latencies
- **Chart.js Integration**: Live graphical plot of API performance
- **Reservoir Sampling**: Maintains up to 10,000 latency samples for accurate percentiles

### Data-Driven Runner

Execute bulk requests from a CSV or JSON file:

- Upload a CSV or JSON array file
- Column names map to `{{variable_names}}` in the request
- Each row triggers a full request execution with variable substitution
- Results table shows iteration, status, and duration

### SQL Sidekick

Run database queries immediately after an API call to verify the data layer:

- **Supported drivers**: SQLite, PostgreSQL, MySQL
- **Auto-detection** of driver from connection string format
- **Security**: Blocks dangerous keywords (DROP, DELETE, TRUNCATE, etc.), Unicode normalization to prevent homoglyph bypasses, blocks multi-statement queries and SQL comments
- **Extract to Var**: Pull a specific cell from your SQL result and save it directly as a variable for the next API call
- **Connection pooling** with LRU eviction and background cleanup
- Max pool size of 50 connections, connections expire after 1 hour

### WebSocket Client

Real-time communication with WebSocket servers:

- Connect to any `ws://` or `wss://` endpoint
- Interactive message log with timestamps and color-coding
- Persistent connection across navigation
- Circular buffer (max 1000 messages) for memory safety
- Automatic reconnection cleanup

### Kafka Message Producer

Produce messages to Apache Kafka clusters directly from the interface:

- **Broker configuration**: Multiple brokers, client ID
- **Security**: TLS with optional skip-verify, SASL (PLAIN, SCRAM-SHA-256, SCRAM-SHA-512)
- **Compression**: None, GZip, Snappy, LZ4, ZSTD
- **Configurable**: Required acks, batch size, batch timeout, write timeout
- **Topic browsing**: List available topics and view partition metadata
- **Message headers**: Custom key-value headers per message
- **Partition targeting**: Explicit partition or hash-based balancing
- **Saved configs**: Store and manage multiple cluster configurations

### Mock Server

Start PostIt with `-mock` flag to enable a local mock server at `/mock/`:

- **Dynamic Routing**: Define paths and route incoming requests
- **Conditional Mocks**: Evaluate GJSON conditions on request body to serve different responses
- **Simulated Latency**: Add artificial delays to mocks for testing slow networks
- **Mock management**: Save, view, and delete mock responses from the request editor
- **Mock dashboard**: View usage statistics per mock

### Visual Workflows

Orchestrate multi-step API sequences with a drag-and-drop canvas:

- **Request Nodes**: Fire API calls and extract data using GJSON paths into workflow variables
- **Condition Nodes (If/Else)**: Branch execution based on GJSON evaluations of the previous response
- **Loop Nodes**: Iterate over a JSON array, injecting each item as `{{$item}}`
- **Wait Nodes**: Introduce artificial delays between steps
- **Script Nodes**: Execute inline JavaScript with the script processor
- **Input Nodes**: Pause workflow execution and wait for a variable to be provided
- **Cycle Detection**: Automatic detection of potential infinite loops (max 1000 visits per node)
- **Task Limit**: Maximum 5000 tasks per workflow execution
- Execution logs with full node-by-node status tracking

### Proxy Server (Observer)

PostIt can act as an HTTP proxy to intercept and record traffic:

- **Start/Stop** from the toolbar
- **Capture**: All outgoing traffic is cloned and saved as new requests
- **Auto-refresh**: Sidebar updates every 5 seconds while the proxy is running
- **SSRF Protection**: Blocks requests to private IP ranges, localhost, and AWS metadata endpoints
- Requests are saved under `Intercepted > [Date] > [Method] [Time]`

### Security Fuzzer

One-click security scan for common vulnerabilities:

- **SQL Injection**: `' OR 1=1 --`, UNION-based, time-based, etc.
- **Cross-Site Scripting (XSS)**: Script injection, img/onerror, svg/onload
- **Robustness**: Edge case inputs (null, undefined, extreme numbers, empty strings)
- **Injection points**: JSON body fields and URL query parameters
- **Concurrent execution**: Bounded to 20 concurrent goroutines
- Results table with field, payload, status code, and response time

### GraphQL Explorer

Dedicated GraphQL editor with schema introspection:

- Split-pane: Query editor + variables editor
- **Schema Introspection**: Automatically fetches and displays remote schema types, fields, and descriptions
- Syntax highlighting for GraphQL queries

### Schema Generation & Validation

- **Generate**: Automatically infer a JSON Schema from any JSON response body
- **Validate**: Validate response bodies against a saved schema
- Supports object, array, string, number, integer, boolean types

### Code Snippets

Generate request code snippets in multiple languages:

- JavaScript (fetch)
- Go (net/http)
- Python (requests)

### API Documentation Generator

Generate beautiful documentation from your collection:

- **Markdown**: Clean markdown with headers, methods, URLs, headers, and body samples
- **HTML**: Self-contained HTML page with styled request cards
- **OpenAPI 3.0**: Standard OpenAPI JSON spec for integration with other tools

### Architecture Visualizer

Generate Mermaid.js sequence diagrams from:

- Request history
- Workflow execution logs

Export as SVG for documentation.

### Global Search & Command Palette

- **Ctrl+P**: Quick Jump — fuzzy-search across all request paths and methods
- **Ctrl+Shift+P**: Command Palette — rapid access to all app functions (JSON formatting, environment switching, etc.)

---

## Import & Export

### Import

| Source | Description |
|--------|-------------|
| **cURL** | Paste any cURL command to instantly create a full request profile |
| **OpenAPI/Swagger** | Upload or paste an OpenAPI JSON/YAML spec to import all endpoints |
| **Postman Collection** | Upload or paste v2.1 Postman collections |

### Export

| Format | Description |
|--------|-------------|
| **Postman Collection** | Export as a Postman-compatible v2.1 collection JSON |
| **History JSON** | Full request history with response data |
| **History CSV** | Tabular history export (timestamp, path, method, URL, status, duration) |
| **API Docs (Markdown)** | Human-readable markdown documentation |
| **API Docs (HTML)** | Self-contained styled HTML documentation |
| **API Docs (OpenAPI)** | Standard OpenAPI 3.0 JSON spec |
| **HAR Export** | HTTP Archive format for history |

---

## Security & Vault

PostIt takes security seriously with multiple built-in protections:

### Credential Vault

- **AES-GCM encryption** with PBKDF2 key derivation (100,000 iterations)
- Master password unlocks the vault; encrypted secrets are stored safely on disk
- Secret variables are stored encrypted and only decrypted on access
- Versioned encryption format for backward compatibility

### SSRF Protection

The proxy server blocks requests to:
- Private IP ranges (10.x, 172.16-31.x, 192.168.x)
- Localhost / 127.0.0.1 / ::1 / 0.0.0.0
- Cloud metadata endpoints (169.254.169.254)
- Non-HTTP/HTTPS schemes

### SQL Injection Prevention

The SQL sidekick validates queries against:
- Dangerous keywords (DROP, DELETE, TRUNCATE, ALTER, UNION, INSERT, UPDATE, EXEC)
- Unicode homoglyph bypasses (Cyrillic lookalike characters)
- Comment obfuscation (`/* */`, `--`, `#`)
- Multiple statements (semicolons mid-query)
- NULL byte injection

### CSRF Protection

All API endpoints (except GET) require valid `Origin` or `Referer` headers matching `localhost` or `127.0.0.1`.

### Body Size Limits

- Default: 10 MB
- Imports (OpenAPI, Postman): 50 MB

---

## Architecture

```
PostIt/
├── cmd/
│   └── postit/
│       └── main.go          # Application entrypoint, CLI flag parsing, mode dispatch
├── internal/
│   ├── api/
│   │   ├── client.go        # HTTP client (resty), request execution, variable resolution
│   │   ├── export.go        # Postman collection export
│   │   ├── fuzzer.go        # Security fuzzer (SQLi, XSS, robustness)
│   │   ├── hammer.go        # Load testing engine (concurrent workers, streaming, percentiles)
│   │   ├── kafka.go         # Kafka message producer (TLS, SASL, compression)
│   │   ├── proxy.go         # HTTP proxy with SSRF protection and traffic capture
│   │   ├── runner.go        # Data-driven runner (CSV/JSON iteration)
│   │   ├── sql.go           # SQL execution engine with security validation
│   │   ├── websocket.go     # WebSocket client with circular buffer
│   │   └── workflow.go      # Workflow orchestration engine (DAG execution)
│   ├── assets/
│   │   └── assets.go        # Embedded frontend assets
│   ├── models/
│   │   ├── postman.go       # Request/collection data models matching Postman v2.1
│   │   ├── history.go       # History record model
│   │   ├── environment.go   # Environment and variable models
│   │   ├── workflow.go      # Workflow DAG models (nodes, edges, extracts)
│   │   ├── kafka.go         # Kafka config and message models
│   │   └── openapi.go       # OpenAPI spec parsing models
│   ├── processor/
│   │   ├── script.go        # JavaScript-like script processor, variable resolution, conditions
│   │   ├── postman.go       # Postman collection parser
│   │   ├── openapi.go       # OpenAPI/Swagger spec parser
│   │   ├── curl.go          # cURL command parser
│   │   └── har.go           # HAR format parser
│   ├── storage/
│   │   ├── manager.go       # File-based storage manager (atomic writes, JSON persistence)
│   │   └── errors.go        # Storage error types
│   ├── tui/
│   │   └── app.go           # Terminal UI application (tview-based)
│   ├── ui/
│   │   └── menu.go          # Interactive CLI menu (survey-based)
│   └── web/
│       ├── server.go        # HTTP server, API routes, CSRF middleware, all handlers
│       ├── server_test.go   # Server tests (race conditions, workflows, CRUD, CSRF, schemas)
│       └── static/          # Web frontend assets
│           ├── index.html   # Main SPA application
│           ├── style.css    # Dark-themed stylesheet
│           ├── app.js       # Frontend logic (5900+ lines, all UI interactions)
│           ├── docs.html    # Built-in feature documentation page
│           └── docs.css     # Documentation styling
└── go.mod                   # Go module definition
```

### Key Design Principles

- **Clean Architecture**: Layered structure with clear separation of concerns
- **Dependency Injection**: No global state; all components receive their dependencies via constructors
- **Concurrency Safety**: `sync.RWMutex` protection on all shared state, atomic operations for counters
- **Local-First**: All data stored as JSON files on disk; no external databases or cloud services required
- **Postman-Compatible**: Full support for Postman v2.1 collection format
- **Embedded Static Assets**: Web UI is compiled into the binary via Go's `embed` package for single-binary distribution

### Storage

All data is persisted as JSON files under the `output/` directory:

| File | Purpose |
|------|---------|
| `output/requests/*.json` | Individual request files (safe filename encoding) |
| `output/variables.json` | Global variables |
| `output/global_headers.json` | Global headers |
| `output/history.json` | Request history (last 50 records) |
| `output/collection.json` | Reconstructed collection tree |
| `output/environments.json` | Environment definitions |
| `output/active_env.json` | Currently active environment ID |
| `output/workflows.json` | Saved workflow definitions |
| `output/kafka_connections.json` | Saved Kafka cluster connections |

All writes use **atomic file operations** (write to `.tmp`, then rename) to prevent data corruption.

---

## Building from Source

### Prerequisites

- Go 1.25.5+

### Build

```bash
# Clone the repository
git clone <repo-url> && cd PostIt

# Build the CLI/web/TUI binary
go build -o postit.exe ./cmd/postit

```

### Dependencies

Key libraries used:

| Library | Purpose |
|---------|---------|
| `resty` | HTTP client |
| `gorilla/websocket` | WebSocket client |
| `segmentio/kafka-go` | Kafka message producer |
| `tview` / `tcell` | Terminal UI |
| `survey` | Interactive CLI prompts |
| `charmbracelet/log` | Structured logging |
| `gjson` | JSON path queries |
| `uuid` | UUID generation |
| `google/uuid` | GUID generation |
| `modernc.org/sqlite` | Embedded SQLite driver |
| `lib/pq` | PostgreSQL driver |
| `go-sql-driver/mysql` | MySQL driver |
| `golang.org/x/crypto` | PBKDF2 key derivation |

---

## Configuration

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-ui` | `web` | UI type: `web`, `tui`, or `cli` |
| `-port` | `8080` | Port for the Web UI |
| `-mock` | `false` | Enable mock server (Web UI only) |
| `-import` | `""` | Comma-separated paths to Postman collection JSON files |

### Run Command Flags

| Flag | Description |
|------|-------------|
| `--collection` | Path to collection JSON (required) |
| `--env` | Path to environment JSON |
| `--delay` | Delay between requests in milliseconds |

---

## Roadmap

### Phase 1: Visual Assertions (The Sandbox)

- **Data Model**: New `Assertion` struct with Target, Property, Operator, Value
- **Targets**: Status Code, JSON Body (GJSON Path), Header, Response Time
- **Operators**: Equals, Contains, Greater Than, Less Than, Exists
- **Execution Engine**: Go-based evaluator returning structured test results
- **UI Integration**: Rule builder with dropdown-based assertion construction; dedicated test results pane (checklist view)

Check `features.txt` for the latest roadmap details.

---

*PostIt — Your API toolbelt, offline and always ready.*
