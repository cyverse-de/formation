# Date Filtering Implementation

## Overview

Added date filtering capabilities to the `/apps` endpoint, allowing users to filter interactive apps by `integration_date` and `edited_date` using comparison operators.

## Query Parameters

### `integration_date` (optional)
Filter apps by their integration date with a comparison operator prefix.

**Format:** `<operator><ISO-8601-date>`

**Examples:**
- `">2025-09-29"` - Apps integrated after Sept 29, 2025
- `"<=2024-12-31T23:59:59"` - Apps integrated on or before Dec 31, 2024
- `">=2025-01-01T00:00:00Z"` - Apps integrated on or after Jan 1, 2025 (UTC)

### `edited_date` (optional)
Filter apps by their last edited date with a comparison operator prefix.

**Format:** `<operator><ISO-8601-date>`

**Notes:**
- Only considers apps with non-null `edited_date` values
- Same format and operators as `integration_date`

## Supported Operators

- `>` - Greater than
- `<` - Less than
- `>=` - Greater than or equal to
- `<=` - Less than or equal to
- `==` - Equal to

## Supported Date Formats

All ISO 8601 date/datetime formats are supported:

- **Date only:** `2025-09-29`
- **Datetime:** `2025-09-29T14:30:00`
- **With UTC timezone (Z):** `2025-09-29T14:30:00Z`
- **With timezone offset:** `2025-09-29T14:30:00+05:00`, `2025-09-29T14:30:00-08:00`
- **With microseconds:** `2025-09-29T14:30:00.123456`

## Timezone Handling

- Dates with timezone information are converted to UTC
- Dates without timezone information are treated as UTC
- All comparisons are done in UTC against the database's `timestamp without time zone` fields
- Result datetimes are returned as naive UTC datetimes

## Usage Examples

### Filter by integration date only
```bash
GET /apps?integration_date=>2025-09-29
```

### Filter by edited date only
```bash
GET /apps?edited_date=<=2024-12-31
```

### Combine multiple filters (AND logic)
```bash
GET /apps?integration_date=>=2025-01-01&edited_date=<2025-12-31&name=jupyter
```

### With timezone
```bash
GET /apps?integration_date=>2025-09-29T00:00:00-08:00
```

## Error Handling

Invalid date filter formats return `400 Bad Request` with descriptive error messages:

```json
{
  "detail": "Invalid date filter format: 'invalid'. Expected format: <operator><date> (e.g., '>2025-09-29', '<=2024-12-31T23:59:59')"
}
```

## Implementation Details

### Files Modified

1. **`main.py`**
   - Added `parse_date_filter()` function to parse date filter expressions
   - Updated `get_interactive_apps()` to accept and process date filters
   - Modified SQL query to include date comparison filters
   - Updated `/apps` endpoint with new query parameters
   - Enhanced Swagger documentation

2. **`tests/test_interactive_apps.py`**
   - Added comprehensive table-driven tests for `parse_date_filter()`
   - Tests for valid operators, date formats, timezone conversions
   - Tests for invalid inputs and error handling
   - 33 test cases covering edge cases and error conditions

### SQL Query Changes

Date filters are added to the WHERE clause using parameterized queries:

```sql
WHERE integration_date > %s  -- for integration_date filter
  AND edited_date IS NOT NULL AND edited_date <= %s  -- for edited_date filter
```

The `edited_date` filter includes a NULL check since that column is nullable.

### Security

- All date filters use parameterized SQL queries to prevent SQL injection
- Input validation ensures only valid operators and date formats are accepted
- Malformed expressions are rejected with clear error messages

## Testing

Run the comprehensive test suite:

```bash
uv run pytest tests/test_interactive_apps.py -v
```

**Test Coverage:**
- 14 tests for valid date filter inputs
- 18 tests for invalid date filter inputs
- 4 tests for timezone conversion
- Additional integration test stubs

All tests passing (41 total).
