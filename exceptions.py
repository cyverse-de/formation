"""Custom exceptions for Formation API."""

from typing import Any


class FormationException(Exception):
    """Base exception for Formation errors."""

    def __init__(
        self,
        message: str,
        status_code: int = 500,
        details: dict[str, Any] | None = None,
    ):
        self.message = message
        self.status_code = status_code
        self.details = details or {}
        super().__init__(self.message)


class ServiceUnavailableError(FormationException):
    """Service is not available or not configured."""

    def __init__(self, service_name: str):
        super().__init__(
            message=f"{service_name} service not configured", status_code=503
        )


class ExternalServiceError(FormationException):
    """External service returned an error."""

    def __init__(self, service_name: str, status_code: int, detail: str):
        super().__init__(
            message=f"{service_name} error: {detail}",
            status_code=status_code,
            details={"service": service_name, "original_error": detail},
        )


class ResourceNotFoundError(FormationException):
    """Requested resource was not found."""

    def __init__(self, resource_type: str, resource_id: str | None = None):
        message = f"{resource_type} not found"
        if resource_id:
            message = f"{resource_type} '{resource_id}' not found"
        super().__init__(message=message, status_code=404)


class ValidationError(FormationException):
    """Input validation failed."""

    def __init__(self, message: str, field: str | None = None):
        details = {"field": field} if field else {}
        super().__init__(message=message, status_code=400, details=details)


class PermissionDeniedError(FormationException):
    """User does not have permission to access resource."""

    def __init__(self, message: str = "Access denied"):
        super().__init__(message=message, status_code=403)
