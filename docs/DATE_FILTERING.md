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


The response also includes a `details` object naming the offending query parameter:

```json
{
  "detail": "Invalid date format: 'not-a-date'. Expected ISO 8601 format (e.g., '2025-09-29', '2025-09-29T14:30:00', '2025-09-29T14:30:00Z')",
  "details": {
    "field": "integration_date"
  }
}
```

## Implementation

Date filters are parsed and applied client-side in `internal/handlers/apputil.go`
(`parseDateFilter`, `dateFilter.matches`), with table-driven tests in
`internal/handlers/apputil_test.go`.
