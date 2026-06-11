# Code Guidelines
* Keep code succinct.
* Add validation both in the backend and frontend.
* Don't repeat yourself needlessly.
* Prefer composition over inheritance for new first-party types.
* Use table-driven tests rather than lots of small, similar tests.
* Add doc comments to publicly available methods and functions.
* Document code succinctly but thoroughly.
* Generally treat warnings as errors unless fixing the warning would cause difficult to fix breakages.
* Run unit tests after changes to make sure there aren't any breakages.
* All backend operations go through the terrain API gateway with the caller's bearer token forwarded; do not call apps, app-exposer, or iRODS directly.
* Only access the database directly if absolutely necessary and ask for permission before adding database access code.
* Formation is the DE's hosted MCP server; the former REST API has been removed. Keep MCP tool output text and error wording stable unless explicitly asked to change them.

# Tooling
* This is a Go project. Use the standard Go toolchain ('go build', 'go test', 'go vet').
* Use 'gofmt' and 'goimports' to format code.
* Use 'golangci-lint run ./...' to lint code.
* If available, use podman when building images instead of Docker ('./build.sh --runtime podman').

# Code Quality and Linting
* After editing Go files, run 'gofmt' and 'golangci-lint run ./...' to catch issues.
* When IDE diagnostics are visible (unused imports, unused variables, type errors), proactively fix them before completing the task.
* Remove unused imports and variables immediately when they're detected.
* Prefer fixing code quality issues proactively rather than waiting for the user to request fixes.

# Layout
* cmd/formation: main entry point and route wiring (landing page, /mcp, OAuth discovery).
* internal/config: config file + environment loading (env > JSON > defaults).
* internal/apierror: typed errors and the JSON error handler.
* internal/auth: Keycloak OIDC token verification and the Caller identity forwarded to terrain.
* internal/authtest: fake Keycloak server for tests.
* internal/clients: terrain HTTP client (apps/analyses/VICE in terrain.go, data endpoints in terrain_data.go).
* internal/terraintest: fake terrain server for tests, plus an in-memory fake of the data endpoints.
* internal/vice: VICE URL readiness checking and subdomain resolution.
* internal/handlers: terrain-backed operations the MCP tools call (*_ops.go), plus the landing page and path-prefix middleware.
* internal/mcp: hosted MCP server at /mcp (tools, Keycloak bearer auth bridge, OAuth discovery metadata + DCR shim).

# Other important projects
* terrain: Usually available at ../terrain. The DE API gateway formation calls for all backend operations.
* portal-conductor: Usually available at ../portal-conductor/. Provides an API for the portal.
* apps: Usually available at ../apps. Provides an API for Discovery Environment app information and operations (behind terrain).
* app-exposer: Usually available at ../app-exposer. Provides an API for the VICE feature in the Discovery Environment (behind terrain).
* data-info: Usually available at ../data-info. Provides the data store API (behind terrain).

# Commands
- 'go run ./cmd/formation' launches the formation server locally (port 8000 by default).
- 'go test ./...' runs the unit tests.
- 'golangci-lint run ./...' lints the code.
