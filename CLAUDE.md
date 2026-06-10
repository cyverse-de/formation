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
* When possible use the apps service API to get information and perform operations.
* If it's not possible through the apps API, then check the app-exposer API and use that if necessary.
* Only access the database directly if absolutely necessary and ask for permission before adding database access code.
* The REST API is a drop-in replacement for the original Python/FastAPI implementation: preserve response JSON shapes, error message strings, and status codes unless explicitly asked to change them.

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
* cmd/formation: main entry point and route wiring.
* internal/config: config file + environment loading (env > JSON > defaults).
* internal/apierror: typed errors and the FastAPI-compatible error handler.
* internal/auth: Keycloak token verification and Echo middlewares.
* internal/authtest: fake Keycloak server for tests.
* internal/clients: apps and app-exposer HTTP clients.
* internal/vice: VICE URL readiness checking and subdomain resolution.
* internal/datastore: iRODS store behind the narrow Store interface.
* internal/handlers: Echo handlers for all endpoints.

# Other important projects
* portal-conductor: Usually available at ../portal-conductor/. Provides an API for the portal.
* apps: Usually available at ../apps. Provides an API for Discovery Environment app information and operations.
* app-exposer: Usually available at ../app-exposer. Provides an API for the VICE feature in the Discovery Environment, which is a subset of the overall apps feature.

# Commands
- 'go run ./cmd/formation' launches the formation server locally (port 8000 by default).
- 'go test ./...' runs the unit tests.
- 'golangci-lint run ./...' lints the code.
- 'go run github.com/swaggo/swag/cmd/swag@latest init -g cmd/formation/main.go -o apidocs --outputTypes go,json' regenerates the Swagger spec (the apidocs package); run it after changing handler annotations or routes.
