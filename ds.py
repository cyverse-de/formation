from typing import Any

from irods.access import iRODSAccess
from irods.exception import UserDoesNotExist
from irods.models import User
from irods.path import iRODSPath
from irods.session import iRODSSession
from irods.user import iRODSUser


class DataStoreAPI:
    _user_type = "rodsuser"

    def __init__(self, host: str, port: str, user: str, password: str, zone: str):
        self.session = iRODSSession(
            host=host, port=port, user=user, password=password, zone=zone
        )
        self.session.connection_timeout = None
        self.host = host
        self.port = port
        self.user = user
        self.zone = zone

    def path_exists(self, a_path: str) -> bool:
        fixed_path = iRODSPath(a_path)
        return self.session.data_objects.exists(
            fixed_path
        ) or self.session.collections.exists(fixed_path)

    def collection_exists(self, path: str) -> bool:
        """Check if an iRODS collection exists at the specified path."""
        return self.session.collections.exists(path)

    def file_exists(self, path: str) -> bool:
        """Check if an iRODS data object exists at the specified path."""
        return self.session.data_objects.exists(path)

    def user_exists(self, username: str) -> bool:
        user_exists = False

        try:
            user = self.session.users.get(username, self.zone)
            user_exists = user is not None
        except UserDoesNotExist:
            user_exists = False

        return user_exists

    def list_users_by_username(self, username: str) -> list[iRODSUser]:
        return [
            self.session.users.get(u[User.name], u[User.zone])
            for u in self.session.query(User).filter(
                User.name == username and User.zone == self.zone
            )
        ]

    def delete_home(self, username: str) -> None:
        homedir = self.home_directory(username)
        if self.session.collections.exists(homedir):
            self.session.collections.remove(homedir, force=True, recurse=True)

    def create_user(self, username: str) -> iRODSUser:
        return self.session.users.create(username, DataStoreAPI._user_type)

    def get_user(self, username: str) -> iRODSUser:
        return self.session.users.get(username, self.zone)

    def delete_user(self, username: str) -> None:
        self.session.users.get(username, self.zone).remove()

    def change_password(self, username: str, password: str) -> None:
        self.session.users.modify(username, "password", password)

    def chmod(self, username: str, permission: str, path: str) -> None:
        access = iRODSAccess(permission, iRODSPath(path), username)
        self.session.acls.set(access)

    def list_available_permissions(self) -> list[str]:
        return self.session.available_permissions.keys()

    def get_permissions(self, path: str) -> list[iRODSAccess]:
        clean_path = iRODSPath(path)

        obj = None
        if self.session.data_objects.exists(clean_path):
            obj = self.session.data_objects.get(clean_path)
        else:
            obj = self.session.collections.get(clean_path)

        return self.session.acls.get(obj)

    def home_directory(self, username: str) -> str:
        return iRODSPath(f"/{self.zone}/home/{username}")

    def _user_has_permission(
        self, username: str, path: str, required_permissions: list[str]
    ) -> bool:
        """Check if user has any of the specified permissions on the path.

        Args:
            username: Username to check permissions for
            path: Path to check permissions on
            required_permissions: List of permission names that satisfy the check
                                 (e.g., ["read", "write", "own"])

        Returns:
            True if user has any of the required permissions, False otherwise
        """
        try:
            permissions = self.get_permissions(path)

            for perm in permissions:
                if (
                    hasattr(perm, "user_name")
                    and perm.user_name == username
                    and hasattr(perm, "access_name")
                    and perm.access_name in required_permissions
                ):
                    return True

            return False

        except Exception:
            # If we can't check permissions, assume no access
            return False

    def user_can_read(self, username: str, path: str) -> bool:
        """Check if user has read permissions on the specified path."""
        return self._user_has_permission(username, path, ["read", "write", "own"])

    def user_can_write(self, username: str, path: str) -> bool:
        """Check if user has write permissions on the specified path."""
        return self._user_has_permission(username, path, ["write", "own"])

    def get_collection(self, path: str):
        """Get an iRODS collection by path."""
        return self.session.collections.get(path)

    def get_file_contents(self, path: str, offset: int = 0, limit: int | None = None) -> dict:
        """Get contents of an iRODS data object with optional paging."""
        data_obj = self.session.data_objects.get(path)

        with data_obj.open('r') as f:
            if offset > 0:
                f.seek(offset)

            if limit:
                content = f.read(limit)
            else:
                content = f.read()

        file_size = getattr(data_obj, 'size', None)
        if file_size is None:
            file_size = len(content) + offset

        return {
            "content": content,
            "offset": offset,
            "size": file_size
        }

    def _format_metadata_as_headers(self, metadata_items, delimiter: str = ",") -> dict[str, str]:
        """Format AVU metadata items as HTTP response headers.

        Args:
            metadata_items: Iterable of AVU metadata items with name, value, and units attributes
            delimiter: Delimiter to use between value and units (default: ',')

        Returns:
            Dictionary of header names to header values
        """
        headers = {}
        for avu in metadata_items:
            header_key = f"X-Datastore-{avu.name}"
            # Combine value and unit (if present) with custom delimiter
            if avu.units:
                header_value = f"{avu.value}{delimiter}{avu.units}"
            else:
                header_value = avu.value
            headers[header_key] = header_value
        return headers

    def get_file_metadata(self, path: str, delimiter: str = ",") -> dict[str, str]:
        """Get AVU metadata for an iRODS data object formatted as response headers."""
        try:
            data_obj = self.session.data_objects.get(path)
            metadata = data_obj.metadata.items()
            return self._format_metadata_as_headers(metadata, delimiter)
        except Exception:
            # If metadata retrieval fails, return empty headers
            return {}

    def get_collection_metadata(self, path: str, delimiter: str = ",") -> dict[str, str]:
        """Get AVU metadata for an iRODS collection formatted as response headers."""
        try:
            collection = self.session.collections.get(path)
            if collection is not None:
                metadata = collection.metadata.items()
                return self._format_metadata_as_headers(metadata, delimiter)
            return {}
        except Exception:
            # If metadata retrieval fails, return empty headers
            return {}

    def create_directory(self, path: str) -> None:
        """Create an iRODS collection (directory)."""
        self.session.collections.create(path)

    def upload_file(self, path: str, content: bytes) -> None:
        """Upload file content to iRODS data object."""
        # Create parent collection if it doesn't exist
        import os

        parent_path = os.path.dirname(path)
        if not self.session.collections.exists(parent_path):
            self.session.collections.create(parent_path)

        # Create or overwrite the data object
        data_obj = self.session.data_objects.create(path, force=True)

        # Write content
        with data_obj.open('w') as f:
            f.write(content)

    def _set_metadata_on_object(
        self, irods_obj, metadata: dict[str, tuple[str, str]], replace: bool = False
    ) -> None:
        """Set AVU metadata on an iRODS object (data object or collection).

        Args:
            irods_obj: iRODS data object or collection
            metadata: Dict mapping attribute names to (value, units) tuples
            replace: If True, clear existing metadata before adding new
        """
        if replace:
            # Clear existing metadata
            for avu in irods_obj.metadata.items():
                irods_obj.metadata.remove(avu)

        # Add new metadata
        for attribute, (value, units) in metadata.items():
            irods_obj.metadata.add(attribute, value, units if units else None)

    def set_file_metadata(
        self, path: str, metadata: dict[str, tuple[str, str]], replace: bool = False
    ) -> None:
        """Set AVU metadata on an iRODS data object.

        Args:
            path: Path to the data object
            metadata: Dict mapping attribute names to (value, units) tuples
            replace: If True, clear existing metadata before adding new
        """
        data_obj = self.session.data_objects.get(path)
        self._set_metadata_on_object(data_obj, metadata, replace)

    def set_collection_metadata(
        self, path: str, metadata: dict[str, tuple[str, str]], replace: bool = False
    ) -> None:
        """Set AVU metadata on an iRODS collection.

        Args:
            path: Path to the collection
            metadata: Dict mapping attribute names to (value, units) tuples
            replace: If True, clear existing metadata before adding new
        """
        collection = self.session.collections.get(path)
        self._set_metadata_on_object(collection, metadata, replace)

    def delete_file(self, path: str, dry_run: bool = False) -> dict[str, Any]:
        """Delete an iRODS data object.

        Args:
            path: Path to the data object
            dry_run: If True, check if deletion would succeed without deleting

        Returns:
            Dictionary with deletion result
        """
        if dry_run:
            # Check if file exists and return what would happen
            exists = self.session.data_objects.exists(path)
            return {
                "path": path,
                "type": "data_object",
                "would_delete": exists,
                "deleted": False,
                "dry_run": True,
            }

        # Actually delete the file
        self.session.data_objects.unlink(path)
        return {
            "path": path,
            "type": "data_object",
            "would_delete": True,
            "deleted": True,
            "dry_run": False,
        }

    def delete_directory(
        self, path: str, recurse: bool = False, dry_run: bool = False
    ) -> dict[str, Any]:
        """Delete an iRODS collection.

        Args:
            path: Path to the collection
            recurse: If True, delete non-empty directories
            dry_run: If True, check if deletion would succeed without deleting

        Returns:
            Dictionary with deletion result and item count if recursive
        """
        collection = self.session.collections.get(path)

        # Count items if recursive
        item_count = 0
        if recurse and collection:
            if hasattr(collection, "subcollections"):
                item_count += len(list(collection.subcollections))
            if hasattr(collection, "data_objects"):
                item_count += len(list(collection.data_objects))

        if dry_run:
            # Check if directory exists
            exists = self.session.collections.exists(path)
            result = {
                "path": path,
                "type": "collection",
                "would_delete": exists,
                "deleted": False,
                "dry_run": True,
            }
            if recurse and item_count > 0:
                result["item_count"] = item_count
            return result

        # Actually delete the directory
        self.session.collections.remove(path, recurse=recurse, force=True)
        result = {
            "path": path,
            "type": "collection",
            "would_delete": True,
            "deleted": True,
            "dry_run": False,
        }
        if recurse and item_count > 0:
            result["item_count"] = item_count
        return result

    def delete_path(
        self, path: str, recurse: bool = False, dry_run: bool = False
    ) -> dict[str, Any]:
        """Delete a file or directory from iRODS.

        Args:
            path: iRODS path to delete
            recurse: Allow deleting non-empty directories
            dry_run: Preview deletion without executing

        Returns:
            Dictionary with deletion result
        """
        # Check if it's a file or directory
        if self.session.data_objects.exists(path):
            return self.delete_file(path, dry_run=dry_run)
        elif self.session.collections.exists(path):
            return self.delete_directory(path, recurse=recurse, dry_run=dry_run)
        else:
            # Path doesn't exist
            return {
                "path": path,
                "type": "unknown",
                "would_delete": False,
                "deleted": False,
                "dry_run": dry_run,
                "error": "Path not found",
            }
