"""Apps routes for Formation API."""

import asyncio
import re
import sys
import time
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

import httpx
from fastapi import APIRouter, Depends

from clients import AppExposerClient, AppsClient
from config import config
from dependencies import extract_user_from_jwt, get_current_user
from exceptions import (
    ServiceUnavailableError,
    ValidationError,
)
from utils import (
    compare_dates,
    is_placeholder_value,
    parse_iso_date_to_datetime,
    strip_user_suffix,
    validate_uuid,
)

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


async def get_analysis_subdomain(
    analysis_id: str,
    max_retries: int = 5,
    retry_delay: float = 1.0,
) -> str | None:
    """Get subdomain for a VICE analysis with retries.

    Attempts to retrieve the subdomain from app-exposer for a given analysis ID.
    Since subdomains are generated asynchronously, this function retries on 404
    errors to allow time for the deployment to become ready.

    Args:
        analysis_id: Analysis UUID
        max_retries: Maximum number of retry attempts (default: 5)
        retry_delay: Seconds to wait between retries (default: 1.0)

    Returns:
        Subdomain string if found, None otherwise
    """
    if not app_exposer_client:
        return None

    try:
        # Get external ID from app-exposer
        external_id_response = await app_exposer_client.get_external_id(
            UUID(analysis_id)
        )
        external_id = external_id_response.get("external_id")

        if not external_id:
            return None

        # Retry getting async data (deployment may not be ready immediately)
        for attempt in range(max_retries):
            try:
                async_data = await app_exposer_client.get_async_data(external_id)
                subdomain = async_data.get("subdomain")

                if subdomain:
                    return subdomain

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
                print(f"Error getting async data: {str(e)}", file=sys.stderr)
                break

    except Exception as e:
        # If we can't get external ID or subdomain, return None
        print(f"Error getting external ID: {str(e)}", file=sys.stderr)

    return None


def should_remove_placeholder_requirements(requirements: list | None) -> bool:
    """Check if requirements list contains placeholder values from Swagger UI.

    Swagger UI generates placeholder requirements with all zero values.
    These should be removed so the apps service uses the app's defaults.

    Args:
        requirements: List of requirement dictionaries from request body

    Returns:
        True if requirements should be removed, False otherwise
    """
    if not requirements or not isinstance(requirements, list):
        return False

    if len(requirements) == 0:
        return False

    # Check if first requirement has all zero values (Swagger placeholder pattern)
    first_req = requirements[0]
    if not isinstance(first_req, dict):
        return False

    placeholder_fields = [
        "step_number",
        "min_cpu_cores",
        "max_cpu_cores",
        "min_memory_limit",
    ]

    return all(
        first_req.get(k) == 0 or first_req.get(k) == 0.0 for k in placeholder_fields
    )


async def generate_analysis_name(
    app_id: str | None,
    username: str,
    system_id: str,
) -> str:
    """Generate a descriptive analysis name based on the app name and timestamp.

    Fetches the app name from the apps service and creates a clean, timestamped
    analysis name. Falls back to "analysis" if the app name cannot be retrieved.

    Args:
        app_id: App UUID string
        username: Username for authentication
        system_id: System identifier for the app

    Returns:
        Generated analysis name in format: "{app-name}-{timestamp}"
    """
    app_name_clean = "analysis"

    if app_id and apps_client:
        try:
            app_data = await apps_client.get_app(
                UUID(app_id), username, system_id=system_id
            )
            app_name = app_data.get("name", "analysis")
            # Clean app name: lowercase, replace non-alphanumeric with hyphens
            app_name_clean = re.sub(r"[^a-z0-9-]+", "-", app_name.lower()).strip("-")
        except Exception:
            # If we can't fetch app name, use generic name
            app_name_clean = "analysis"

    # Generate timestamp
    timestamp = datetime.now().strftime("%Y-%m-%d-%H%M%S")
    return f"{app_name_clean}-{timestamp}"


def generate_output_directory(
    output_zone: str,
    username: str,
    analysis_name: str,
) -> str:
    """Generate default output directory path for an analysis.

    Creates an iRODS path in the format:
    /{output_zone}/home/{username}/analyses/{analysis-name}

    Args:
        output_zone: iRODS zone name
        username: Username for the directory path
        analysis_name: Name of the analysis

    Returns:
        Full iRODS path for the analysis output directory
    """
    return f"/{output_zone}/home/{username}/analyses/{analysis_name}"


def resolve_user_email(
    email_from_body: str, user: dict[str, Any], username: str
) -> str:
    """Resolve the user's email address for analysis submission.

    Email is resolved in priority order:
    1. If body contains valid email (not empty, not placeholder): use it
    2. If JWT token contains email: use it
    3. Otherwise: construct from username + user_suffix

    Args:
        email_from_body: Email from request body (may be empty or placeholder)
        user: JWT token user data (may contain email)
        username: Username for email construction fallback

    Returns:
        Resolved email address
    """
    if is_placeholder_value(email_from_body):
        # Try to get email from JWT token, fall back to constructing from username
        email = user.get("email")
        if not email:
            # Construct email from username if not in token
            return f"{username}{config.user_suffix}"
        else:
            return email
    return email_from_body


async def prepare_submission_dict(
    submission: dict[str, Any],
    app_id: str,
    system_id: str,
    user: dict[str, Any],
    username: str,
    output_zone: str,
) -> tuple[dict[str, Any], str]:
    """Prepare submission dictionary with defaults and validation.

    Args:
        submission: Raw submission from request body
        app_id: App UUID string
        system_id: System identifier
        user: JWT token user data
        username: Extracted username
        output_zone: iRODS zone for output

    Returns:
        Tuple of (prepared submission dict, email for query param)
    """
    submission_dict = dict(submission)
    submission_dict["app_id"] = app_id

    # Resolve email
    email_from_body = submission_dict.get("email", "")
    submission_dict["email"] = resolve_user_email(email_from_body, user, username)

    # Resolve system_id
    if is_placeholder_value(submission_dict.get("system_id")):
        submission_dict["system_id"] = system_id

    # Set defaults
    submission_dict.setdefault("debug", False)
    submission_dict.setdefault("notify", True)
    submission_dict.setdefault("config", {})

    # Generate name if needed
    if is_placeholder_value(submission_dict.get("name")):
        submission_dict["name"] = await generate_analysis_name(
            submission_dict.get("app_id"), username, system_id
        )

    # Generate output_dir if needed
    if is_placeholder_value(submission_dict.get("output_dir")):
        analysis_name = submission_dict.get("name", "analysis")
        submission_dict["output_dir"] = generate_output_directory(
            output_zone, username, analysis_name
        )

    # Remove placeholder requirements
    if should_remove_placeholder_requirements(submission_dict.get("requirements")):
        submission_dict.pop("requirements", None)

    email_for_query = str(submission_dict.pop("email"))
    return submission_dict, email_for_query


router = APIRouter(prefix="", tags=["Apps"])


# Initialize clients
apps_client: AppsClient | None = None
app_exposer_client: AppExposerClient | None = None

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
        dt = dt.astimezone(UTC).replace(tzinfo=None)

    # Map == to SQL =
    sql_operator = "=" if operator == "==" else operator

    return (sql_operator, dt)


def format_app_for_response(app: dict[str, Any]) -> dict[str, Any]:
    """Format an app from apps service to formation's simpler format.

    Args:
        app: App dictionary from apps service

    Returns:
        Formatted app dictionary
    """
    integrator_name = strip_user_suffix(app.get("integrator_name"), config.user_suffix)

    return {
        "id": app.get("id"),
        "name": app.get("name"),
        "description": app.get("description"),
        "version": app.get("version"),
        "integrator_username": integrator_name,
        "integration_date": app.get("integration_date"),
        "edited_date": app.get("edited_date"),
        "system_id": app.get("system_id"),
        "overall_job_type": app.get("overall_job_type"),
    }


def apply_date_filter(
    apps: list[dict[str, Any]],
    date_field: str,
    date_filter: tuple[str, datetime] | None,
) -> list[dict[str, Any]]:
    """Filter apps by date field using comparison operator.

    Args:
        apps: List of app dictionaries
        date_field: Name of date field to filter on
        date_filter: Tuple of (operator, datetime) or None

    Returns:
        Filtered list of apps
    """
    if not date_filter:
        return apps

    operator, dt = date_filter
    filtered_apps = []

    for app in apps:
        if app.get(date_field):
            app_date = parse_iso_date_to_datetime(app[date_field])
            if compare_dates(app_date, operator, dt):
                filtered_apps.append(app)

    return filtered_apps


def filter_apps(
    apps: list[dict[str, Any]],
    description: str | None,
    integrator: str | None,
    integration_date_filter: tuple[str, datetime] | None,
    edited_date_filter: tuple[str, datetime] | None,
) -> list[dict[str, Any]]:
    """Apply client-side filters to apps list.

    Args:
        apps: List of apps to filter
        description: Description substring filter
        integrator: Integrator name filter
        integration_date_filter: Integration date filter tuple
        edited_date_filter: Edited date filter tuple

    Returns:
        Filtered list of apps
    """
    # Apply description filter
    if description:
        apps = [
            app
            for app in apps
            if description.lower() in (app.get("description") or "").lower()
        ]

    # Apply integrator filter
    if integrator:
        integrator_search = strip_user_suffix(integrator, config.user_suffix)
        apps = [
            app
            for app in apps
            if integrator_search
            and integrator_search.lower() in (app.get("integrator_name") or "").lower()
        ]

    # Apply date filters
    apps = apply_date_filter(apps, "integration_date", integration_date_filter)
    apps = apply_date_filter(apps, "edited_date", edited_date_filter)

    return apps


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
    interacting with the app through other endpoints (e.g., `/apps/{system_id}/{app_id}/parameters`,
    `/app/launch/{system_id}/{app_id}`, etc.).

    Each app includes an `overall_job_type` field indicating the type of app:
    - "Interactive": VICE (Visual Interactive Computing Environment) apps
    - "DE": Discovery Environment batch apps
    - "OSG": Open Science Grid apps
    - "Tapis": High-Performance Computing apps
    """
    # Validate pagination parameters
    if limit < 1 or limit > 1000:
        raise ValidationError("Limit must be between 1 and 1000", field="limit")
    if offset < 0:
        raise ValidationError("Offset must be non-negative", field="offset")

    if not apps_client:
        raise ServiceUnavailableError("Apps")

    username = extract_user_from_jwt(current_user)

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
    apps = filter_apps(
        apps, description, integrator, integration_date_filter, edited_date_filter
    )

    # Apply pagination after filtering
    total = len(apps)
    apps = apps[offset : offset + limit]

    # Transform to formation's simpler format
    formatted_apps = [format_app_for_response(app) for app in apps]

    return {"total": total, "apps": formatted_apps}


@router.get("/apps/analyses/")
async def list_analyses(
    status: str = "Running",
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """List analyses for the authenticated user, filtered by status.

    Returns analyses filtered by the specified status. By default, returns only
    running analyses.

    Args:
        status: Status filter (default: "Running"). Common values:
                - "Running": Currently executing analyses
                - "Completed": Successfully finished analyses
                - "Failed": Analyses that failed
                - "Submitted": Queued but not yet running
                - "Canceled": User-canceled analyses

    Returns:
        Dictionary with 'analyses' list, where each analysis contains:
        - analysis_id: Analysis UUID
        - app_id: App UUID that was launched
        - system_id: System identifier (e.g., 'de', 'hpc')
        - status: Current status
    """
    if not apps_client:
        raise ServiceUnavailableError("Apps")

    username = extract_user_from_jwt(user)

    # Get analyses from apps service with status filter
    result = await apps_client.list_analyses(username, status=status)

    # Extract and format the analyses list
    analyses = result.get("analyses", [])
    formatted_analyses = []
    for analysis in analyses:
        formatted_analyses.append(
            {
                "analysis_id": analysis.get("id"),
                "app_id": analysis.get("app_id"),
                "system_id": analysis.get("system_id"),
                "status": analysis.get("status"),
            }
        )

    return {"analyses": formatted_analyses}


@router.get("/apps/{system_id}/{app_id}/parameters")
async def get_app_parameters(
    system_id: str,
    app_id: str,
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Get parameter definitions for a specific app.

    Returns the parameter groups and definitions needed to launch the app,
    including parameter types, labels, default values, and validation rules.

    Args:
        system_id: System identifier from the app listing (use the `system_id` field
                   returned by the `/apps` endpoint)
        app_id: App UUID

    Returns:
        Dictionary containing:
        - groups: List of parameter groups, each with:
            - id: Group identifier
            - name: Internal group name
            - label: Display label for the group
            - parameters: List of parameter definitions
            - step_number: Step number for multi-step apps
    """
    if not apps_client:
        raise ServiceUnavailableError("Apps")

    username = extract_user_from_jwt(user)

    # Convert string to UUID
    app_uuid = validate_uuid(app_id, "app_id")

    # Get full app definition from apps service
    app_data = await apps_client.get_app(app_uuid, username, system_id=system_id)

    # Extract and return the groups section which contains parameter definitions
    # Include overall_job_type to help clients determine if it's an interactive app
    return {
        "groups": app_data.get("groups", []),
        "overall_job_type": app_data.get("overall_job_type"),
    }


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
        raise ServiceUnavailableError("Apps")

    # Use configured output zone if not provided
    if output_zone is None:
        output_zone = config.output_zone

    username = extract_user_from_jwt(user)

    # Validate app_id format
    validate_uuid(app_id, "app_id")

    # Create empty submission if body is None
    if submission is None:
        submission = {}

    # Prepare submission with defaults and validation
    submission_dict, email_for_query = await prepare_submission_dict(
        submission, app_id, system_id, user, username, output_zone
    )

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
    if analysis_id:
        subdomain = await get_analysis_subdomain(analysis_id)
        if subdomain:
            result["url"] = f"https://{subdomain}{config.vice_domain}"

    return result


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
        raise ServiceUnavailableError("Required services")

    username = extract_user_from_jwt(user)

    # Validate analysis_id format
    analysis_uuid = validate_uuid(analysis_id, "analysis_id")

    # Get analysis info from apps service
    analysis = await apps_client.get_analysis(analysis_uuid, username)

    # Get subdomain from app-exposer for VICE apps (with retries)
    subdomain = await get_analysis_subdomain(analysis_id)
    url_ready = False
    url_check_details = {}

    # Check URL readiness by directly probing the VICE URL
    if subdomain:
        url = f"https://{subdomain}{config.vice_domain}"
        url_ready, url_check_details = await check_vice_url_ready(url)

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


@router.post("/apps/analyses/{analysis_id}/control")
async def control_app(
    analysis_id: str,
    operation: str,
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Control a running analysis (extend time, save & exit, exit).

    Supports actions like extending the time limit, saving outputs and
    exiting, or exiting without saving.

    Args:
        analysis_id: Analysis UUID (returned by the `/app/launch/{system_id}/{app_id}` endpoint)
        operation: Control operation to perform (extend_time, save_and_exit, or exit)
    """
    del user  # Unused but required for authentication

    if not app_exposer_client:
        raise ServiceUnavailableError("App-exposer")

    valid_operations = ["extend_time", "save_and_exit", "exit"]
    if operation not in valid_operations:
        raise ValidationError(
            f"Invalid operation. Must be one of: {', '.join(valid_operations)}",
            field="operation",
        )

    # Validate analysis_id format
    analysis_uuid = validate_uuid(analysis_id, "analysis_id")

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
