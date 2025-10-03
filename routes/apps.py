"""Apps routes for Formation API."""

import asyncio
import re
import sys
import time
from datetime import datetime, timezone
from typing import Any, Optional
from uuid import UUID

import httpx
from fastapi import APIRouter, Depends, HTTPException

from clients import AppExposerClient, AppsClient
from config import config
from dependencies import extract_user_from_jwt, get_current_user

# Simple in-memory cache for VICE URL checks
# Format: {url: (timestamp, ready, details)}
_vice_url_cache: dict[str, tuple[float, bool, dict[str, Any]]] = {}


async def check_vice_url_ready(url: str) -> tuple[bool, dict[str, Any]]:
    """
    Check if a VICE app URL is ready by probing it with HTTP requests.

    Uses caching and retry logic with exponential backoff to reliably
    determine if the URL is accessible.

    Args:
        url: Full VICE app URL (e.g., https://subdomain.cyverse.run)

    Returns:
        Tuple of (ready: bool, details: dict with status_code, response_time_ms, error)
    """
    # Check cache first
    current_time = time.time()
    if url in _vice_url_cache:
        cached_time, cached_ready, cached_details = _vice_url_cache[url]
        if current_time - cached_time < config.vice_url_check_cache_ttl:
            return cached_ready, cached_details

    # Perform URL check with retries
    retries = config.vice_url_check_retries
    timeout = config.vice_url_check_timeout

    for attempt in range(retries):
        try:
            start_time = time.time()

            # Try HEAD request first (lighter weight), fall back to GET
            async with httpx.AsyncClient(
                timeout=timeout, follow_redirects=False
            ) as client:
                # Try HEAD first
                try:
                    response = await client.head(url)
                    # If HEAD returns 405 Method Not Allowed or 404, try GET
                    if response.status_code in (404, 405):
                        response = await client.get(url)
                except httpx.ConnectError:
                    # Connection failed, let outer exception handler deal with it
                    raise
                except Exception:
                    # HEAD failed for some other reason, try GET
                    response = await client.get(url)

                response_time_ms = int((time.time() - start_time) * 1000)

                # Consider 2xx and 3xx as "ready"
                ready = 200 <= response.status_code < 400

                details = {
                    "status_code": response.status_code,
                    "response_time_ms": response_time_ms,
                    "attempt": attempt + 1,
                }

                # Cache the result
                _vice_url_cache[url] = (current_time, ready, details)

                return ready, details

        except httpx.TimeoutException:
            if attempt < retries - 1:
                # Exponential backoff: 0.5s, 1s, 2s, etc.
                await asyncio.sleep(0.5 * (2**attempt))
                continue
            details = {
                "error": "timeout",
                "timeout_seconds": timeout,
                "attempt": attempt + 1,
            }
            _vice_url_cache[url] = (current_time, False, details)
            return False, details

        except Exception as e:
            if attempt < retries - 1:
                await asyncio.sleep(0.5 * (2**attempt))
                continue
            details = {
                "error": str(e),
                "error_type": type(e).__name__,
                "attempt": attempt + 1,
            }
            _vice_url_cache[url] = (current_time, False, details)
            return False, details

    # Should never reach here, but just in case
    details = {"error": "max_retries_exceeded", "retries": retries}
    _vice_url_cache[url] = (current_time, False, details)
    return False, details


router = APIRouter(prefix="", tags=["Apps"])


# Initialize clients
apps_client: Optional[AppsClient] = None
app_exposer_client: Optional[AppExposerClient] = None

if config.apps_base_url:
    apps_client = AppsClient(base_url=config.apps_base_url)

if config.app_exposer_base_url:
    app_exposer_client = AppExposerClient(base_url=config.app_exposer_base_url)


# Job type definitions and mappings
# Maps user-facing job type names to internal names used by the apps service
JOB_TYPE_ALIASES = {
    "vice": "Interactive",
    "interactive": "Interactive",
    "de": "DE",
    "osg": "OSG",
    "tapis": "Tapis",
}

# Job type metadata for the /apps/job-types endpoint
JOB_TYPES = [
    {
        "name": "VICE",
        "description": "Visual Interactive Computing Environment applications",
        "internal_name": "Interactive",
    },
    {
        "name": "DE",
        "description": "Discovery Environment batch applications",
        "internal_name": "DE",
    },
    {
        "name": "OSG",
        "description": "Open Science Grid applications",
        "internal_name": "OSG",
    },
    {
        "name": "Tapis",
        "description": "High-Performance Computing applications",
        "internal_name": "Tapis",
    },
]


def normalize_job_type(job_type: str | None) -> str | None:
    """
    Normalize a job type parameter to the internal name used by the apps service.

    Accepts user-facing aliases (e.g., "VICE", "vice") and returns the internal
    name (e.g., "Interactive"). Case-insensitive.

    Args:
        job_type: Job type from user input (can be None)

    Returns:
        Internal job type name, or None if input was None
    """
    if job_type is None:
        return None

    # Case-insensitive lookup
    normalized = JOB_TYPE_ALIASES.get(job_type.lower())
    if normalized:
        return normalized

    # If not found in aliases, return as-is (allows for future job types)
    return job_type


def parse_date_filter(filter_expr: str) -> tuple[str, datetime]:
    """
    Parse a date filter expression like ">2025-09-29" into SQL operator and datetime.

    Args:
        filter_expr: Date filter expression with operator prefix (e.g., ">2025-09-29",
                    "<=2024-12-31T23:59:59", "==2025-01-01T00:00:00Z")

    Returns:
        Tuple of (SQL operator, datetime object as naive UTC datetime)

    Raises:
        ValueError: If the expression format is invalid or date cannot be parsed
    """
    # Match operator followed by optional whitespace and ISO date
    # Supports: >, <, >=, <=, ==
    pattern = r"^(>=|<=|==|>|<)\s*(.+)$"
    match = re.match(pattern, filter_expr.strip())

    if not match:
        raise ValueError(
            f"Invalid date filter format: '{filter_expr}'. "
            "Expected format: <operator><date> (e.g., '>2025-09-29', '<=2024-12-31T23:59:59')"
        )

    operator, date_str = match.groups()

    # Parse the date using fromisoformat (supports various ISO 8601 formats)
    try:
        dt = datetime.fromisoformat(date_str.strip())
    except ValueError as e:
        raise ValueError(
            f"Invalid date format: '{date_str}'. Expected ISO 8601 format "
            "(e.g., '2025-09-29', '2025-09-29T14:30:00', '2025-09-29T14:30:00Z')"
        ) from e

    # Convert to naive UTC datetime for database comparison
    # PostgreSQL stores timestamp without time zone, so we need naive datetimes
    if dt.tzinfo is not None:
        # Convert to UTC then strip timezone info
        dt = dt.astimezone(timezone.utc).replace(tzinfo=None)

    # Map == to SQL =
    sql_operator = "=" if operator == "==" else operator

    return (sql_operator, dt)


def compare_dates(app_date: datetime, operator: str, filter_date: datetime) -> bool:
    """
    Compare two dates using the specified operator.

    Args:
        app_date: Date from the app (with or without timezone)
        operator: Comparison operator (>, <, >=, <=, =)
        filter_date: Date from the filter (naive UTC datetime)

    Returns:
        True if the comparison matches, False otherwise
    """
    # Convert app_date to naive UTC if it has timezone info
    if app_date.tzinfo is not None:
        app_date = app_date.astimezone(timezone.utc).replace(tzinfo=None)

    if operator == ">":
        return app_date > filter_date
    elif operator == "<":
        return app_date < filter_date
    elif operator == ">=":
        return app_date >= filter_date
    elif operator == "<=":
        return app_date <= filter_date
    elif operator == "=":
        return app_date == filter_date
    else:
        return False


@router.get("/apps/job-types")
async def list_job_types() -> dict[str, Any]:
    """List valid job types for filtering apps.

    Returns the job type values that can be used with the job_type
    parameter of the GET /apps endpoint. Includes both user-facing
    names (like "VICE") and their internal equivalents.
    """
    return {"job_types": JOB_TYPES}


@router.get("/apps")
async def list_apps(
    current_user: Any = Depends(get_current_user),
    limit: int = 100,
    offset: int = 0,
    name: str | None = None,
    description: str | None = None,
    integrator: str | None = None,
    integration_date: str | None = None,
    edited_date: str | None = None,
    job_type: str | None = None,
) -> dict[str, Any]:
    """List apps available to the user.

    Returns a list of apps that the user has access to, optionally filtered by job type.

    The returned `system_id` field should be used as the `system_id` path parameter when
    interacting with the app through other endpoints (e.g., `/apps/{system_id}/{app_id}/config`,
    `/app/launch/{system_id}/{app_id}`, etc.).
    """
    # Validate pagination parameters
    if limit < 1 or limit > 1000:
        raise HTTPException(status_code=400, detail="Limit must be between 1 and 1000")
    if offset < 0:
        raise HTTPException(status_code=400, detail="Offset must be non-negative")

    if not apps_client:
        raise HTTPException(status_code=503, detail="Apps service not configured")

    username = extract_user_from_jwt(current_user)

    try:
        # Normalize job type (e.g., "VICE" -> "Interactive")
        normalized_job_type = normalize_job_type(job_type)

        # Build search term from name filter if provided
        search_term = name if name else None

        # Parse date filters if provided
        integration_date_filter = None
        edited_date_filter = None
        if integration_date:
            integration_date_filter = parse_date_filter(integration_date)
        if edited_date:
            edited_date_filter = parse_date_filter(edited_date)

        # Get apps from apps service with a larger limit to allow for client-side filtering
        # We'll filter and paginate on the client side
        response = await apps_client.list_apps(
            username=username,
            limit=1000,  # Get more to allow filtering
            offset=0,
            search=search_term,
        )

        apps = response.get("apps", [])

        # Filter by job type if specified (using normalized value)
        if normalized_job_type:
            apps = [
                app
                for app in apps
                if app.get("overall_job_type") == normalized_job_type
            ]

        # Apply client-side filters
        if description:
            apps = [
                app
                for app in apps
                if description.lower() in (app.get("description") or "").lower()
            ]

        if integrator:
            # Strip user suffix if provided
            integrator_search = integrator
            if config.user_suffix and integrator_search.endswith(config.user_suffix):
                integrator_search = integrator_search[: -len(config.user_suffix)]

            apps = [
                app
                for app in apps
                if integrator_search.lower()
                in (app.get("integrator_name") or "").lower()
            ]

        if integration_date_filter:
            operator, dt = integration_date_filter
            apps = [
                app
                for app in apps
                if app.get("integration_date")
                and compare_dates(
                    datetime.fromisoformat(
                        app["integration_date"].replace("Z", "+00:00")
                    ),
                    operator,
                    dt,
                )
            ]

        if edited_date_filter:
            operator, dt = edited_date_filter
            apps = [
                app
                for app in apps
                if app.get("edited_date")
                and compare_dates(
                    datetime.fromisoformat(app["edited_date"].replace("Z", "+00:00")),
                    operator,
                    dt,
                )
            ]

        # Apply pagination after filtering
        total = len(apps)
        apps = apps[offset : offset + limit]

        # Transform to formation's simpler format
        formatted_apps = []
        for app in apps:
            # Remove user suffix from integrator name
            integrator_name = app.get("integrator_name")
            if integrator_name and config.user_suffix:
                if integrator_name.endswith(config.user_suffix):
                    integrator_name = integrator_name[: -len(config.user_suffix)]

            formatted_apps.append(
                {
                    "id": app.get("id"),
                    "name": app.get("name"),
                    "description": app.get("description"),
                    "version": app.get("version"),
                    "integrator_username": integrator_name,
                    "integration_date": app.get("integration_date"),
                    "edited_date": app.get("edited_date"),
                    "system_id": app.get("system_id"),
                }
            )

        return {"total": total, "apps": formatted_apps}

    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except httpx.HTTPError as e:
        raise HTTPException(status_code=503, detail=f"Apps service error: {str(e)}")


@router.get("/apps/{system_id}/{app_id}/config")
async def get_app_config(
    system_id: str,
    app_id: str,
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Get configuration for a specific app.

    Returns the configuration needed to launch the app, including
    parameter definitions and default values.

    Args:
        system_id: System identifier from the app listing (use the `system_id` field
                   returned by the `/apps` endpoint)
        app_id: App UUID
    """
    if not apps_client:
        raise HTTPException(status_code=503, detail="Apps service not configured")

    username = extract_user_from_jwt(user)

    try:
        # Convert string to UUID
        app_uuid = UUID(app_id)

        # Get full app definition from apps service
        app_data = await apps_client.get_app(app_uuid, username, system_id=system_id)

        # Extract and return just the config section
        # The config section contains parameter definitions needed for launching
        config_section = app_data.get("config", {})

        return config_section
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid app ID format")
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(status_code=404, detail="App not found")
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Apps service error: {e.response.text}",
        )
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to retrieve app config: {str(e)}"
        )


@router.post("/app/launch/{system_id}/{app_id}")
async def launch_app(
    system_id: str,
    app_id: str,
    submission: dict[str, Any] | None = None,
    user: Any = Depends(get_current_user),
    output_zone: str | None = None,
) -> dict[str, Any]:
    """Launch an app.

    Submits an analysis to launch the specified app with the provided
    configuration. Returns the analysis ID and optionally the URL once
    the deployment is ready (for interactive/VICE apps).

    Args:
        system_id: System identifier from the app listing (use the `system_id` field
                   returned by the `/apps` endpoint)
        app_id: App UUID
        submission: Optional analysis submission parameters
        output_zone: iRODS zone for output directory. If not specified, defaults to the
                     configured OUTPUT_ZONE setting. The output directory will be created
                     at /{output_zone}/home/{username}/analyses/{analysis-name}
    """
    if not apps_client:
        raise HTTPException(status_code=503, detail="Apps service not configured")

    # Use configured output zone if not provided
    if output_zone is None:
        output_zone = config.output_zone

    username = extract_user_from_jwt(user)

    # Validate app_id format (outside try block to avoid catching it)
    try:
        UUID(app_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid app ID format")

    try:
        # Create empty submission if body is None
        if submission is None:
            submission = {}

        # Convert to regular dict for internal processing
        submission_dict: dict[str, Any] = dict(submission)

        # Inject app_id from path parameter
        submission_dict["app_id"] = app_id

        # Add defaults for optional fields
        # email: Extract from JWT token if not provided, or if provided value is placeholder
        email_from_body = submission_dict.get("email", "")
        if not email_from_body or email_from_body == "string":
            # Try to get email from JWT token, fall back to constructing from username
            email = user.get("email")
            if not email:
                # Construct email from username if not in token
                submission_dict["email"] = f"{username}{config.user_suffix}"
            else:
                submission_dict["email"] = email

        # system_id: Use the provided system_id parameter if not in submission body
        system_id_from_body = submission_dict.get("system_id", "")
        if not system_id_from_body or system_id_from_body == "string":
            submission_dict["system_id"] = system_id

        # debug: Default to False (don't retain inputs for debugging)
        if "debug" not in submission_dict:
            submission_dict["debug"] = False

        # notify: Default to True (send email notifications on completion)
        if "notify" not in submission_dict:
            submission_dict["notify"] = True

        # config: Default to empty dict (no parameter overrides)
        if "config" not in submission_dict:
            submission_dict["config"] = {}

        # name: Generate descriptive name if not provided or if placeholder
        name_from_body = submission_dict.get("name", "")
        if not name_from_body or name_from_body == "string":
            # Fetch app details to get app name (already validated above)
            app_id_for_name = submission_dict.get("app_id")
            if app_id_for_name:
                try:
                    app_data = await apps_client.get_app(
                        UUID(app_id_for_name), username, system_id=system_id
                    )
                    app_name = app_data.get("name", "analysis")
                    # Clean app name: lowercase, replace non-alphanumeric with hyphens
                    app_name_clean = re.sub(
                        r"[^a-z0-9-]+", "-", app_name.lower()
                    ).strip("-")
                except Exception:
                    # If we can't fetch app name, use generic name
                    app_name_clean = "analysis"
            else:
                app_name_clean = "analysis"

            # Generate timestamp
            timestamp = datetime.now().strftime("%Y-%m-%d-%H%M%S")
            submission_dict["name"] = f"{app_name_clean}-{timestamp}"

        # output_dir: Generate default if not provided or if placeholder
        output_dir_from_body = submission_dict.get("output_dir", "")
        if not output_dir_from_body or output_dir_from_body == "string":
            # Generate default output directory based on username and analysis name
            analysis_name = submission_dict.get("name", "analysis")
            # Default output dir format: /{output_zone}/home/{username}/analyses/{analysis-name}
            submission_dict["output_dir"] = (
                f"/{output_zone}/home/{username}/analyses/{analysis_name}"
            )

        # requirements: Remove if it's a placeholder value
        requirements_from_body = submission_dict.get("requirements", [])
        if requirements_from_body and isinstance(requirements_from_body, list):
            # Check if it's the placeholder from Swagger UI (has all zero values)
            if len(requirements_from_body) > 0:
                first_req = requirements_from_body[0]
                if isinstance(first_req, dict) and all(
                    first_req.get(k) == 0 or first_req.get(k) == 0.0
                    for k in [
                        "step_number",
                        "min_cpu_cores",
                        "max_cpu_cores",
                        "min_memory_limit",
                    ]
                ):
                    # It's a placeholder, remove it so apps service uses app defaults
                    submission_dict.pop("requirements", None)

        # Extract email for query parameter (apps service expects it in query params, not body)
        email_for_query = str(submission_dict.pop("email"))

        response = await apps_client.submit_analysis(
            submission_dict, username, email_for_query
        )

        # Extract analysis ID
        analysis_id = response.get("id")

        # Construct minimal response
        result = {
            "analysis_id": analysis_id,
            "name": response.get("name", submission_dict.get("name", "Unnamed")),
            "status": response.get("status", "Submitted"),
        }

        # Try to get subdomain for URL (with retries since it's generated asynchronously)
        if app_exposer_client:
            try:
                # Get external ID from app-exposer
                external_id_response = await app_exposer_client.get_external_id(
                    UUID(analysis_id)
                )
                external_id = external_id_response.get("external_id")

                if external_id:
                    # Retry getting async data (deployment may not be ready immediately)
                    max_retries = 5
                    retry_delay = 1.0  # seconds

                    for attempt in range(max_retries):
                        try:
                            async_data = await app_exposer_client.get_async_data(
                                external_id
                            )
                            subdomain = async_data.get("subdomain")

                            if subdomain:
                                result["url"] = (
                                    f"https://{subdomain}{config.vice_domain}"
                                )
                                break  # Success, exit retry loop

                        except httpx.HTTPStatusError as e:
                            # 404 means deployment not ready yet, retry
                            if e.response.status_code == 404:
                                if attempt < max_retries - 1:
                                    await asyncio.sleep(retry_delay)
                                    continue
                            # Other errors, log and give up
                            print(
                                f"Error getting async data: {e.response.status_code}",
                                file=sys.stderr,
                            )
                            break
                        except Exception as e:
                            print(
                                f"Error getting async data: {str(e)}", file=sys.stderr
                            )
                            break

            except Exception as e:
                # If we can't get external ID or subdomain, just omit URL from response
                print(f"Error getting external ID: {str(e)}", file=sys.stderr)

        return result
    except httpx.HTTPStatusError as e:
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Apps service error: {e.response.text}",
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Launch failed: {str(e)}")


@router.get("/apps/analyses/{analysis_id}/status")
async def get_app_status(
    analysis_id: str,
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Get the status of a running analysis.

    Returns the current status of the analysis, including whether
    the interactive app is ready and its URL (for VICE apps).

    The `url_ready` field indicates if the VICE app URL is accessible by
    directly probing it with HTTP requests (with retries and caching).
    Additional details about the URL check (status code, response time, errors)
    are included in the `url_check_details` field.

    Args:
        analysis_id: Analysis UUID (returned by the `/app/launch/{system_id}/{app_id}` endpoint)
    """
    if not apps_client or not app_exposer_client:
        raise HTTPException(status_code=503, detail="Required services not configured")

    username = extract_user_from_jwt(user)

    try:
        # Get analysis info from apps service
        analysis_uuid = UUID(analysis_id)
        analysis = await apps_client.get_analysis(analysis_uuid, username)

        # Get subdomain from app-exposer for VICE apps
        subdomain = None
        url_ready = False
        url_check_details = {}

        try:
            # Get external ID from app-exposer
            external_id_response = await app_exposer_client.get_external_id(
                analysis_uuid
            )
            external_id = external_id_response.get("external_id")

            if external_id:
                # Get async data (including subdomain)
                async_data = await app_exposer_client.get_async_data(external_id)
                subdomain = async_data.get("subdomain")

                # Check URL readiness by directly probing the VICE URL
                if subdomain:
                    url = f"https://{subdomain}{config.vice_domain}"
                    url_ready, url_check_details = await check_vice_url_ready(url)
        except httpx.HTTPStatusError as e:
            # If we get 404, the VICE app may not exist or not be ready yet
            if e.response.status_code != 404:
                print(
                    f"Error getting VICE URL info: {e.response.status_code}",
                    file=sys.stderr,
                )
        except Exception as e:
            print(f"Error getting VICE URL info: {str(e)}", file=sys.stderr)

        result = {
            "analysis_id": analysis_id,
            "status": analysis.get("status", "Unknown"),
            "url_ready": url_ready,
        }

        if subdomain:
            result["url"] = f"https://{subdomain}{config.vice_domain}"

        # Include URL check details if available (status code, response time, errors)
        if url_check_details:
            result["url_check_details"] = url_check_details

        return result
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(status_code=404, detail="Analysis not found")
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Service error: {e.response.text}",
        )
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid analysis ID format")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Status check failed: {str(e)}")


@router.post("/apps/analyses/{analysis_id}/control")
async def control_app(
    analysis_id: str,
    operation: str,
    _user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Control a running analysis (extend time, save & exit, exit).

    Supports actions like extending the time limit, saving outputs and
    exiting, or exiting without saving.

    Args:
        analysis_id: Analysis UUID (returned by the `/app/launch/{system_id}/{app_id}` endpoint)
        operation: Control operation to perform (extend_time, save_and_exit, or exit)
    """
    if not app_exposer_client:
        raise HTTPException(
            status_code=503, detail="App-exposer service not configured"
        )

    valid_operations = ["extend_time", "save_and_exit", "exit"]
    if operation not in valid_operations:
        raise HTTPException(
            status_code=400,
            detail=f"Invalid operation. Must be one of: {', '.join(valid_operations)}",
        )

    try:
        analysis_uuid = UUID(analysis_id)

        result: dict[str, Any]
        if operation == "extend_time":
            result = await app_exposer_client.extend_time_limit(analysis_uuid)
            result["operation"] = operation
        elif operation == "save_and_exit":
            result = await app_exposer_client.save_and_exit(analysis_uuid)
            result["operation"] = operation
        else:  # operation == "exit"
            result = await app_exposer_client.exit_without_save(analysis_uuid)
            result["operation"] = operation

        return result
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(status_code=404, detail="Analysis not found")
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Service error: {e.response.text}",
        )
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid analysis ID format")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Operation failed: {str(e)}")
