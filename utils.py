"""Utility functions for Formation API."""

from datetime import UTC, datetime
from uuid import UUID

from exceptions import ValidationError


def validate_uuid(uuid_str: str, field_name: str) -> UUID:
    """Validate and convert a UUID string.

    Args:
        uuid_str: UUID string to validate
        field_name: Field name for error messages (e.g., "app_id", "analysis_id")

    Returns:
        UUID object

    Raises:
        ValidationError: If UUID format is invalid
    """
    try:
        return UUID(uuid_str)
    except ValueError:
        # Format field name for user-friendly error message: "app_id" -> "app ID"
        # Keep "ID" uppercase, capitalize first letter of other words
        parts = field_name.split("_")
        formatted_parts = [part.upper() if part == "id" else part for part in parts]
        display_name = " ".join(formatted_parts)
        raise ValidationError(f"Invalid {display_name} format", field=field_name)


def strip_user_suffix(username: str | None, suffix: str = "") -> str | None:
    """Remove user suffix from username if present.

    Args:
        username: Username that may have suffix
        suffix: Suffix to remove (e.g., "@iplantcollaborative.org")

    Returns:
        Username without suffix, or None if input was None
    """
    if not username or not suffix:
        return username
    if username.endswith(suffix):
        return username[: -len(suffix)]
    return username


def is_placeholder_value(value: str | None) -> bool:
    """Check if a value is a placeholder (empty or Swagger default).

    Args:
        value: String value to check

    Returns:
        True if value is placeholder, False otherwise
    """
    return not value or value == "string"


def parse_iso_date_to_datetime(iso_date_str: str) -> datetime:
    """Parse ISO 8601 date string to datetime object.

    Handles the 'Z' suffix by converting to '+00:00' format.

    Args:
        iso_date_str: ISO 8601 date string (may have 'Z' suffix)

    Returns:
        Parsed datetime object
    """
    return datetime.fromisoformat(iso_date_str.replace("Z", "+00:00"))


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
        app_date = app_date.astimezone(UTC).replace(tzinfo=None)

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
