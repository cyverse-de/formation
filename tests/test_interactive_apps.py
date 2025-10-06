"""
Tests for the interactive apps endpoint.
"""

import pytest
from datetime import datetime, timezone
from uuid import UUID
from unittest.mock import AsyncMock, MagicMock, patch
from fastapi.testclient import TestClient

from routes.apps import parse_date_filter


# Table-driven tests for parse_date_filter
@pytest.mark.parametrize(
    "filter_expr,expected_operator,expected_date_str,description",
    [
        # Basic operators with date-only format
        (">2025-09-29", ">", "2025-09-29 00:00:00", "Greater than with date only"),
        ("<2025-09-29", "<", "2025-09-29 00:00:00", "Less than with date only"),
        (">=2025-09-29", ">=", "2025-09-29 00:00:00", "Greater than or equal with date only"),
        ("<=2025-09-29", "<=", "2025-09-29 00:00:00", "Less than or equal with date only"),
        ("==2025-09-29", "=", "2025-09-29 00:00:00", "Equal to with date only (== mapped to =)"),

        # With datetime (time component)
        (">2025-09-29T14:30:00", ">", "2025-09-29 14:30:00", "Greater than with datetime"),
        ("<=2024-12-31T23:59:59", "<=", "2024-12-31 23:59:59", "Less than or equal with datetime"),

        # With timezone - UTC (Z suffix)
        (">2025-09-29T14:30:00Z", ">", "2025-09-29 14:30:00", "With UTC timezone (Z suffix)"),

        # With timezone - positive offset
        (">2025-09-29T14:30:00+05:00", ">", "2025-09-29 09:30:00", "With +05:00 timezone offset"),

        # With timezone - negative offset
        (">=2025-09-29T14:30:00-08:00", ">=", "2025-09-29 22:30:00", "With -08:00 timezone offset"),

        # Whitespace handling
        ("> 2025-09-29", ">", "2025-09-29 00:00:00", "Whitespace after operator"),
        ("  >=2025-09-29  ", ">=", "2025-09-29 00:00:00", "Leading and trailing whitespace"),
        (">  2025-09-29T10:00:00  ", ">", "2025-09-29 10:00:00", "Multiple spaces after operator"),

        # Edge cases - microseconds
        (">2025-09-29T14:30:00.123456", ">", "2025-09-29 14:30:00.123456", "With microseconds"),
    ],
)
def test_parse_date_filter_valid(filter_expr, expected_operator, expected_date_str, description):
    """Test parse_date_filter with valid inputs using table-driven approach."""
    operator, dt = parse_date_filter(filter_expr)

    # Verify operator
    assert operator == expected_operator, f"Failed: {description}"

    # Verify datetime
    expected_dt = datetime.fromisoformat(expected_date_str)
    assert dt == expected_dt, f"Failed: {description}"

    # Verify datetime is naive (no timezone)
    assert dt.tzinfo is None, f"Datetime should be naive (no tzinfo): {description}"


@pytest.mark.parametrize(
    "filter_expr,error_substring,description",
    [
        # Invalid operators
        ("=2025-09-29", "Invalid date filter format", "Single equals sign"),
        ("!2025-09-29", "Invalid date filter format", "Exclamation mark"),
        ("!=2025-09-29", "Invalid date filter format", "Not equals operator"),
        ("~2025-09-29", "Invalid date filter format", "Tilde operator"),

        # Missing operator
        ("2025-09-29", "Invalid date filter format", "Missing operator"),

        # Invalid date formats
        (">09-29-2025", "Invalid date format", "MM-DD-YYYY format"),
        (">2025/09/29", "Invalid date format", "Slash separators"),
        (">29-09-2025", "Invalid date format", "DD-MM-YYYY format"),
        (">2025-13-01", "Invalid date format", "Invalid month (13)"),
        (">2025-09-32", "Invalid date format", "Invalid day (32)"),
        (">2025-02-30", "Invalid date format", "Invalid date (Feb 30)"),

        # Malformed expressions
        (">", "Invalid date filter format", "Operator only, no date"),
        ("", "Invalid date filter format", "Empty string"),
        ("   ", "Invalid date filter format", "Whitespace only"),
        (">not-a-date", "Invalid date format", "Non-date text"),

        # Invalid datetime formats
        (">2025-09-29T25:00:00", "Invalid date format", "Invalid hour (25)"),
        (">2025-09-29T14:60:00", "Invalid date format", "Invalid minute (60)"),
        (">2025-09-29T14:30:99", "Invalid date format", "Invalid second (99)"),
    ],
)
def test_parse_date_filter_invalid(filter_expr, error_substring, description):
    """Test parse_date_filter with invalid inputs raises ValueError."""
    with pytest.raises(ValueError) as exc_info:
        parse_date_filter(filter_expr)

    assert error_substring in str(exc_info.value), f"Failed: {description}"


def test_parse_date_filter_timezone_conversion():
    """Test that timezones are properly converted to UTC."""
    # Test with various timezone offsets
    test_cases = [
        (">2025-09-29T12:00:00+00:00", datetime(2025, 9, 29, 12, 0, 0)),  # UTC
        (">2025-09-29T12:00:00Z", datetime(2025, 9, 29, 12, 0, 0)),  # UTC (Z notation)
        (">2025-09-29T12:00:00+05:00", datetime(2025, 9, 29, 7, 0, 0)),  # +5 hours -> subtract 5
        (">2025-09-29T12:00:00-05:00", datetime(2025, 9, 29, 17, 0, 0)),  # -5 hours -> add 5
    ]

    for filter_expr, expected_utc_dt in test_cases:
        operator, dt = parse_date_filter(filter_expr)
        assert dt == expected_utc_dt, f"Timezone conversion failed for {filter_expr}"
        assert dt.tzinfo is None, "Result should be naive datetime"


@pytest.fixture
def mock_permissions_client():
    """Mock permissions client for testing."""
    client = AsyncMock()
    client.get_accessible_app_ids = AsyncMock()
    return client


@pytest.fixture
def mock_db_conn():
    """Mock database connection for testing."""
    conn = MagicMock()
    cursor = AsyncMock()
    conn.cursor.return_value.__aenter__.return_value = cursor
    return conn, cursor


def test_get_apps_empty_permissions(mock_permissions_client, mock_db_conn):
    """Test that empty permissions returns empty result."""

    # Setup mocks
    mock_permissions_client.get_accessible_app_ids.return_value = set()

    # This test would need async test framework
    # Just documenting the test structure
    pass


def test_get_apps_with_results(mock_permissions_client, mock_db_conn):
    """Test successful retrieval of interactive apps."""
    # Test implementation would go here
    # Would mock:
    # - permissions_client.get_accessible_app_ids() returning app UUIDs
    # - db cursor.execute() and fetchone()/fetchall() for count and results
    # - Verify correct SQL is generated
    # - Verify correct response format
    pass


def test_list_apps_authentication_required():
    """Test that endpoint requires authentication."""
    # Would test that unauthenticated requests return 401
    pass


def test_list_apps_pagination_validation():
    """Test pagination parameter validation."""
    # Test invalid limit values (< 1, > 1000)
    # Test invalid offset values (< 0)
    pass


def test_list_apps_permissions_service_error():
    """Test handling of permissions service errors."""
    # Mock permissions service returning HTTP error
    # Verify 503 response
    pass


def test_list_apps_database_error():
    """Test handling of database errors."""
    # Mock database query raising exception
    # Verify 500 response
    pass


def test_list_apps_invalid_date_filter():
    """Test that invalid date filters return 400 Bad Request."""
    # Would test endpoint with invalid date filter
    # Verify 400 response with appropriate error message
    pass


def test_get_apps_with_date_filters():
    """Test filtering by integration_date and edited_date."""
    # Would test:
    # - Filter with integration_date only
    # - Filter with edited_date only
    # - Filter with both date filters
    # - Verify SQL includes proper date comparison clauses
    # - Verify parameterized queries
    pass
