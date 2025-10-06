# Code Guidelines
* Keep code succinct.
* Add validation both in the backend and frontend.
* Don't repeat yourself needlessly.
* Don't use multiple inheritance.
* Prefer composition over inheritance for new first-party types.
* Use table-driven tests rather than lots of small, similar tests.
* Add doc comments to publicly available methods and functions.
* Document code succinctly but thoroughly.
* Use type hinting in Python.
* Generally treat warnings as errors unless fixing the warning would cause difficult to fix breakages.
* Run unit tests after changes to make sure there aren't any breakages.
* When possible use the apps service API to get information and perform operations.
* If it's not possible through the apps API, then check the app-exposer API and use that if necessary.
* Only access the database directly if absolutely necessary and ask for permission before adding database access code.


# Tooling
* Use 'uv' for building, running, and managing Python projects.
* If available, use podman when building images instead of Docker.

# Code Quality and Linting
* After editing Python files, run 'uv run ruff check --fix <file>' to auto-fix linting issues.
* When IDE diagnostics are visible (unused imports, unused variables, type errors), proactively fix them before completing the task.
* Remove unused imports and variables immediately when they're detected.
* For required-but-unused parameters (e.g., in FastAPI dependencies), use explicit 'del variable' statements instead of underscore prefixes to satisfy linters.
* Prefer fixing code quality issues proactively rather than waiting for the user to request fixes.

# Other important projects
* portal-conductor: Usually available at ../portal-conductor/. Provides an API for the portal.
* apps: Usually available at ../apps. Provides an API for Discovery Environment app information and operations.
* app-exposer: Usually available at ../app-exposer. Provides an API for the VICE feature in the Discovery Environment, which is a subset of the overall apps feature.

# Commands
- 'uv run fastapi run main.py' launches the formation server locally.
- 'uv sync' will update the dependencies locally.
- 'uv add <dependency>' will add a new dependency.
- 'uv cache clean' will delete the local cache of dependencies.


