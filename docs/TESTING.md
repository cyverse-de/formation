# Formation Testing Guide

This document describes the testing strategy for Formation and provides instructions for running tests.

## Testing Strategy

Formation uses a three-tier testing approach:

1. **Unit Tests** - Fast, isolated tests with mocked dependencies
2. **Integration Tests** - Tests against live services (iRODS, apps, app-exposer)
3. **Manual Tests** - End-to-end workflows for comprehensive validation

## Running Tests

### Unit Tests

Unit tests run quickly and don't require external services:

```bash
# Run all unit tests
uv run pytest tests/unit/ -v

# Run specific test file
uv run pytest tests/unit/test_clients.py -v

# Run with coverage report
uv run pytest tests/unit/ --cov=. --cov-report=html

# Run only tests matching a pattern
uv run pytest tests/unit/ -k "test_launch"
```

### Integration Tests

Integration tests require live services to be running and configured.

**Prerequisites:**
- iRODS server accessible with credentials in env vars
- Apps service running and accessible
- App-exposer service running and accessible
- Keycloak server for authentication

**Configuration:**
Set these environment variables before running integration tests:

```bash
export DB_HOST=your-db-host
export DB_PORT=5432
export DB_USER=de
export DB_PASSWORD=your-password
export DB_NAME=de
export IRODS_HOST=your-irods-host
export IRODS_PORT=1247
export IRODS_USER=your-service-account
export IRODS_PASSWORD=your-irods-password
export IRODS_ZONE=iplant
export KEYCLOAK_SERVER_URL=https://your-keycloak.example.com
export KEYCLOAK_REALM=CyVerse
export KEYCLOAK_CLIENT_ID=formation
export KEYCLOAK_CLIENT_SECRET=your-secret
export APPS_BASE_URL=https://de.cyverse.org/api/apps
export APP_EXPOSER_BASE_URL=https://de.cyverse.org/api/app-exposer
```

**Run integration tests:**
```bash
# Run all integration tests
uv run pytest tests/integration/ -v -m integration

# Run only iRODS integration tests
uv run pytest tests/integration/ -v -m irods

# Run only apps service integration tests
uv run pytest tests/integration/ -v -m apps
```

## Manual Testing Plan

Manual tests validate end-to-end workflows that are difficult to automate or require human verification.

### Test Environment Setup

1. **Start Formation server:**
   ```bash
   # Set all required environment variables (see above)
   uv run fastapi dev main.py
   ```

2. **Obtain authentication token:**
   ```bash
   # Login to get JWT token
   curl -X POST "http://localhost:8000/login" \
     -u "your-username:your-password" \
     | jq -r '.access_token'

   # Save token for subsequent requests
   export TOKEN="your-jwt-token-here"
   ```

### Test Suite 1: Authentication

**Test 1.1: Successful Login**
```bash
curl -X POST "http://localhost:8000/login" \
  -u "valid-username:valid-password"

Expected: 200 OK with access_token, token_type, expires_in
```

**Test 1.2: Invalid Credentials**
```bash
curl -X POST "http://localhost:8000/login" \
  -u "invalid:wrong"

Expected: 401 Unauthorized with error detail
```

**Test 1.3: Missing Authentication**
```bash
curl "http://localhost:8000/data/browse/iplant/home/shared"

Expected: 403 Forbidden (no bearer token provided)
```

### Test Suite 2: Data Operations

**Test 2.1: Browse Root Directory**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/your-username"

Expected: JSON with type="collection", contents array with files/folders
```

**Test 2.2: Read File Contents**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/your-username/test-file.txt"

Expected: Raw file contents with appropriate Content-Type header
```

**Test 2.3: Browse with Metadata**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/your-username/test-file.txt?include_metadata=true"

Expected: File contents + X-Datastore-* headers with AVU metadata
```

**Test 2.4: Read with Pagination**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/your-username/large-file.txt?offset=100&limit=50"

Expected: 50 bytes starting from byte 100
```

**Test 2.5: Access Denied**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/other-user/private-file.txt"

Expected: 403 Forbidden - Access denied
```

**Test 2.6: Path Not Found**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/data/browse/iplant/home/nonexistent/path"

Expected: 404 Not Found - Path not found
```

### Test Suite 3: App Launch

**Test 3.1: Launch Interactive App**
```bash
curl -X POST "http://localhost:8000/app/launch" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "app_id": "your-app-uuid",
    "name": "Test Analysis",
    "config": {}
  }'

Expected: 200 OK with analysis_id, name, status, url
Save analysis_id for subsequent tests
```

**Test 3.2: Launch with Invalid App ID**
```bash
curl -X POST "http://localhost:8000/app/launch" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "app_id": "00000000-0000-0000-0000-000000000000",
    "name": "Invalid App"
  }'

Expected: 4xx error (app not found or invalid)
```

### Test Suite 4: App Status Monitoring

**Test 4.1: Check Status - Launching**
```bash
# Immediately after launch
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/app/$ANALYSIS_ID/status"

Expected: status="Submitted" or "Launching", url_ready=false
```

**Test 4.2: Check Status - Running**
```bash
# Wait 1-2 minutes, then check again
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/app/$ANALYSIS_ID/status"

Expected: status="Running", url_ready=true, url present
Verify: Open url in browser to confirm app is accessible
```

**Test 4.3: Check Status - Invalid ID**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/app/not-a-uuid/status"

Expected: 400 Bad Request - Invalid analysis ID format
```

**Test 4.4: Check Status - Nonexistent Analysis**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/app/00000000-0000-0000-0000-000000000000/status"

Expected: 404 Not Found - Analysis not found
```

### Test Suite 5: App Control Operations

**Test 5.1: Extend Time Limit**
```bash
curl -X POST \
  "http://localhost:8000/app/$ANALYSIS_ID/control?operation=extend_time" \
  -H "Authorization: Bearer $TOKEN"

Expected: 200 OK with operation="extend_time", time_limit timestamp
Verify: New time_limit is ~3 days in the future
```

**Test 5.2: Get Current Time Limit**
```bash
# Via app-exposer directly to verify extension worked
curl "http://app-exposer-host/vice/admin/analyses/$ANALYSIS_ID/time-limit"

Expected: time_limit matches extended value from 5.1
```

**Test 5.3: Save and Exit**
```bash
curl -X POST \
  "http://localhost:8000/app/$ANALYSIS_ID/control?operation=save_and_exit" \
  -H "Authorization: Bearer $TOKEN"

Expected: 200 OK with operation="save_and_exit", status="terminated", outputs_saved=true
Wait 30 seconds for cleanup
```

**Test 5.4: Verify Termination**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/app/$ANALYSIS_ID/status"

Expected: status shows "Completed" or similar terminal state
Verify: URL no longer accessible (503 or similar)
```

**Test 5.5: Exit Without Save**
```bash
# Launch another analysis first
curl -X POST \
  "http://localhost:8000/app/$NEW_ANALYSIS_ID/control?operation=exit" \
  -H "Authorization: Bearer $TOKEN"

Expected: 200 OK with outputs_saved=false
```

**Test 5.6: Invalid Operation**
```bash
curl -X POST \
  "http://localhost:8000/app/$ANALYSIS_ID/control?operation=invalid_op" \
  -H "Authorization: Bearer $TOKEN"

Expected: 400 Bad Request - Invalid operation
```

**Test 5.7: Control Nonexistent Analysis**
```bash
curl -X POST \
  "http://localhost:8000/app/00000000-0000-0000-0000-000000000000/control?operation=extend_time" \
  -H "Authorization: Bearer $TOKEN"

Expected: 404 Not Found
```

### Test Suite 6: Error Handling

**Test 6.1: Service Unavailable - Apps**
```bash
# Stop apps service, then:
curl -X POST "http://localhost:8000/app/launch" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{}'

Expected: 503 Service Unavailable or 500 with connection error
```

**Test 6.2: Service Unavailable - App-Exposer**
```bash
# Stop app-exposer service, then:
curl -X POST \
  "http://localhost:8000/app/$ANALYSIS_ID/control?operation=extend_time" \
  -H "Authorization: Bearer $TOKEN"

Expected: 503 or 500 with connection error
```

**Test 6.3: Malformed JSON**
```bash
curl -X POST "http://localhost:8000/app/launch" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{invalid json'

Expected: 422 Unprocessable Entity
```

## Test Checklist

Use this checklist during manual testing:

### Pre-Testing
- [ ] All environment variables set correctly
- [ ] Formation server started successfully
- [ ] Apps service accessible
- [ ] App-exposer service accessible
- [ ] iRODS accessible
- [ ] Valid user credentials available

### Authentication (Suite 1)
- [ ] 1.1 Successful login works
- [ ] 1.2 Invalid credentials rejected
- [ ] 1.3 Missing auth rejected

### Data Operations (Suite 2)
- [ ] 2.1 Can browse directories
- [ ] 2.2 Can read files
- [ ] 2.3 Metadata included when requested
- [ ] 2.4 Pagination works correctly
- [ ] 2.5 Access control enforced
- [ ] 2.6 404 for missing paths

### App Launch (Suite 3)
- [ ] 3.1 Can launch app successfully
- [ ] 3.2 Invalid app rejected

### App Status (Suite 4)
- [ ] 4.1 Status shows launching state
- [ ] 4.2 Status shows running + URL ready
- [ ] 4.3 Invalid UUID format rejected
- [ ] 4.4 Nonexistent analysis returns 404

### App Control (Suite 5)
- [ ] 5.1 Can extend time limit
- [ ] 5.2 Time limit actually extended
- [ ] 5.3 Save and exit works
- [ ] 5.4 App actually terminated
- [ ] 5.5 Exit without save works
- [ ] 5.6 Invalid operations rejected
- [ ] 5.7 Nonexistent analysis rejected

### Error Handling (Suite 6)
- [ ] 6.1 Apps service outage handled
- [ ] 6.2 App-exposer outage handled
- [ ] 6.3 Malformed input rejected

## Test Reports

After completing manual tests, document results:

```
Date: YYYY-MM-DD
Tester: Your Name
Environment: dev/staging/production
Formation Version: git commit hash

Results:
- Total tests: X
- Passed: Y
- Failed: Z
- Blocked: W

Failed Tests:
- Test X.Y: Description of failure
- Test A.B: Description of failure

Notes:
- Any observations or issues discovered
```

## Continuous Integration

For automated CI/CD:

```bash
# Run fast unit tests on every commit
uv run pytest tests/unit/ -v --tb=short

# Run integration tests on pull requests (requires live services)
uv run pytest tests/integration/ -v -m integration --tb=short

# Generate coverage report
uv run pytest tests/unit/ --cov=. --cov-report=xml --cov-report=term
```

## Troubleshooting Tests

**Issue: Import errors during tests**
```bash
# Ensure test dependencies installed
uv sync --group test
```

**Issue: Integration tests fail with connection errors**
- Verify all service URLs are accessible
- Check firewall rules allow connections
- Verify credentials are correct and not expired

**Issue: Mocked tests fail unexpectedly**
- Check that patches target the correct import path
- Verify mock return values match expected structure
- Use `pytest -v -s` to see print statements for debugging
