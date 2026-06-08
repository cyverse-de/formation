// Package datastore wraps the CyVerse Go iRODS client to provide the
// file-and-metadata operations Formation exposes as MCP tools. Permission and
// existence checks mirror the original Python implementation.
package datastore

import (
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/types"

	"github.com/cyverse-de/formation/internal/apperr"
)

// DataStore performs iRODS operations on behalf of an authenticated user.
type DataStore struct {
	fs     *fs.FileSystem
	logger *slog.Logger
}

// Config holds the iRODS connection parameters.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Zone     string
}

// Entry is a single item in a directory listing.
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// BrowseResult is the result of browsing a path: either a directory listing or
// file content, plus optional metadata.
type BrowseResult struct {
	Path     string
	Type     string // "collection" or "data_object"
	Contents []Entry
	Content  []byte
	Size     int64
	Offset   int
	Metadata map[string]string
}

// WriteResult describes a create/update operation outcome.
type WriteResult struct {
	Path    string
	Type    string
	Created bool
}

// DeleteResult describes a delete (or dry-run) outcome.
type DeleteResult struct {
	Path        string
	Type        string
	WouldDelete bool
	Deleted     bool
	DryRun      bool
	ItemCount   int
}

const (
	typeCollection = "collection"
	typeDataObject = "data_object"
)

// New connects to iRODS and returns a DataStore. Call Close to release the
// connection pool.
func New(cfg Config, logger *slog.Logger) (*DataStore, error) {
	account, err := types.CreateIRODSAccount(cfg.Host, cfg.Port, cfg.User, cfg.Zone, types.AuthSchemeNative, cfg.Password, "")
	if err != nil {
		return nil, err
	}
	filesystem, err := fs.NewFileSystemWithDefault(account, "formation")
	if err != nil {
		return nil, err
	}
	return &DataStore{fs: filesystem, logger: logger}, nil
}

// Close releases iRODS connections.
func (d *DataStore) Close() {
	if d.fs != nil {
		d.fs.Release()
	}
}

// Browse lists a directory or reads a file at the path after verifying the user
// has read access.
func (d *DataStore) Browse(username, p string, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	p = normalizePath(p)

	entry, err := d.fs.Stat(p)
	if err != nil {
		if types.IsFileNotFoundError(err) {
			return nil, apperr.NotFound("Path", p)
		}
		return nil, d.fail("stat", err)
	}

	if !d.canRead(username, p) {
		return nil, apperr.PermissionDenied()
	}

	if entry.Type == fs.FileEntry {
		return d.readFile(p, entry, offset, limit, includeMetadata, avuDelimiter)
	}
	return d.listDir(p, includeMetadata, avuDelimiter)
}

func (d *DataStore) readFile(p string, entry *fs.Entry, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	handle, err := d.fs.OpenFile(p, "", "r")
	if err != nil {
		return nil, d.fail("open file", err)
	}
	defer func() { _ = handle.Close() }()

	if offset > 0 {
		if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
			return nil, d.fail("seek", err)
		}
	}
	var reader io.Reader = handle
	if limit > 0 {
		reader = io.LimitReader(handle, int64(limit))
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, d.fail("read file", err)
	}

	result := &BrowseResult{
		Path:    p,
		Type:    typeDataObject,
		Content: content,
		Size:    entry.Size,
		Offset:  offset,
	}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(p, avuDelimiter)
	}
	return result, nil
}

func (d *DataStore) listDir(p string, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	entries, err := d.fs.List(p)
	if err != nil {
		return nil, d.fail("list", err)
	}
	contents := make([]Entry, 0, len(entries))
	for _, e := range entries {
		contents = append(contents, Entry{Name: e.Name, Type: entryType(e)})
	}
	result := &BrowseResult{Path: p, Type: typeCollection, Contents: contents}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(p, avuDelimiter)
	}
	return result, nil
}

// CreateDirectory creates a collection (or sets metadata on an existing one).
func (d *DataStore) CreateDirectory(username, p string, metadata []AVU) (*WriteResult, error) {
	p = normalizePath(p)

	if d.fs.Exists(p) {
		if !d.canWrite(username, p) {
			return nil, apperr.PermissionDenied()
		}
		if err := d.applyMetadata(p, metadata, false); err != nil {
			return nil, err
		}
		return &WriteResult{Path: p, Type: typeCollection, Created: false}, nil
	}

	parent := path.Dir(p)
	if !d.fs.Exists(parent) {
		return nil, apperr.NotFound("Parent directory", parent)
	}
	if !d.canWrite(username, parent) {
		return nil, apperr.PermissionDenied()
	}
	if err := d.fs.MakeDir(p, false); err != nil {
		return nil, d.fail("make dir", err)
	}
	if err := d.applyMetadata(p, metadata, false); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeCollection, Created: true}, nil
}

// UploadFile creates or overwrites a data object with the given content.
func (d *DataStore) UploadFile(username, p string, content []byte, metadata []AVU, replaceMetadata bool) (*WriteResult, error) {
	p = normalizePath(p)
	exists := d.fs.Exists(p)

	if exists {
		if d.fs.ExistsDir(p) {
			return nil, apperr.BadRequest("Cannot upload file - path is a directory")
		}
		if !d.canWrite(username, p) {
			return nil, apperr.PermissionDenied()
		}
	} else {
		parent := path.Dir(p)
		if !d.fs.Exists(parent) {
			return nil, apperr.NotFound("Parent directory", parent)
		}
		if !d.canWrite(username, parent) {
			return nil, apperr.PermissionDenied()
		}
	}

	if err := d.writeFile(p, content); err != nil {
		return nil, err
	}
	if err := d.applyMetadata(p, metadata, replaceMetadata); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeDataObject, Created: !exists}, nil
}

func (d *DataStore) writeFile(p string, content []byte) error {
	handle, err := d.fs.CreateFile(p, "", "w")
	if err != nil {
		return d.fail("create file", err)
	}
	if _, err := handle.Write(content); err != nil {
		_ = handle.Close()
		return d.fail("write file", err)
	}
	if err := handle.Close(); err != nil {
		return d.fail("close file", err)
	}
	return nil
}

// SetMetadata sets (optionally replacing) AVU metadata on an existing path.
func (d *DataStore) SetMetadata(username, p string, metadata []AVU, replace bool) (*WriteResult, error) {
	p = normalizePath(p)
	if !d.fs.Exists(p) {
		return nil, apperr.NotFound("Path", p)
	}
	if !d.canWrite(username, p) {
		return nil, apperr.PermissionDenied()
	}
	t := typeDataObject
	if d.fs.ExistsDir(p) {
		t = typeCollection
	}
	if err := d.applyMetadata(p, metadata, replace); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: t, Created: false}, nil
}

// Delete removes a file or directory, supporting dry-run and recursive deletion.
func (d *DataStore) Delete(username, p string, recurse, dryRun bool) (*DeleteResult, error) {
	p = normalizePath(p)
	if !d.fs.Exists(p) {
		return nil, apperr.NotFound("Path", p)
	}
	if !d.canWrite(username, p) {
		return nil, apperr.PermissionDenied()
	}

	if d.fs.ExistsFile(p) {
		return d.deleteFile(p, dryRun)
	}
	return d.deleteDir(p, recurse, dryRun)
}

func (d *DataStore) deleteFile(p string, dryRun bool) (*DeleteResult, error) {
	result := &DeleteResult{Path: p, Type: typeDataObject, WouldDelete: true, DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	if err := d.fs.RemoveFile(p, true); err != nil {
		return nil, d.fail("remove file", err)
	}
	result.Deleted = true
	return result, nil
}

func (d *DataStore) deleteDir(p string, recurse, dryRun bool) (*DeleteResult, error) {
	entries, err := d.fs.List(p)
	if err != nil {
		return nil, d.fail("list", err)
	}
	itemCount := len(entries)

	if !recurse && !dryRun && itemCount > 0 {
		return nil, apperr.BadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
	}

	result := &DeleteResult{Path: p, Type: typeCollection, WouldDelete: true, DryRun: dryRun}
	if recurse && itemCount > 0 {
		result.ItemCount = itemCount
	}
	if dryRun {
		return result, nil
	}
	if err := d.fs.RemoveDir(p, recurse, true); err != nil {
		return nil, d.fail("remove dir", err)
	}
	result.Deleted = true
	return result, nil
}

// applyMetadata sets AVU metadata, optionally clearing existing metadata first.
func (d *DataStore) applyMetadata(p string, metadata []AVU, replace bool) error {
	if len(metadata) == 0 && !replace {
		return nil
	}
	if replace {
		existing, err := d.fs.ListMetadata(p)
		if err != nil {
			return d.fail("list metadata", err)
		}
		for _, m := range existing {
			if err := d.fs.DeleteMetadataByAVU(p, m.Name, m.Value, m.Units); err != nil {
				return d.fail("delete metadata", err)
			}
		}
	}
	for _, avu := range metadata {
		if err := d.fs.AddMetadata(p, avu.Attribute, avu.Value, avu.Units); err != nil {
			return d.fail("add metadata", err)
		}
	}
	return nil
}

func (d *DataStore) metadataHeaders(p, delimiter string) map[string]string {
	metas, err := d.fs.ListMetadata(p)
	if err != nil {
		// Metadata is best-effort, matching the original behavior of returning
		// empty headers on failure.
		d.logf("could not list metadata; returning none", "path", p, "error", err)
		return map[string]string{}
	}
	return formatMetadataHeaders(metas, delimiter)
}

func (d *DataStore) canRead(username, p string) bool  { return d.checkAccess(username, p, readLevels) }
func (d *DataStore) canWrite(username, p string) bool { return d.checkAccess(username, p, writeLevels) }

func (d *DataStore) checkAccess(username, p string, required []types.IRODSAccessLevelType) bool {
	accesses, err := d.fs.ListACLs(p)
	if err != nil {
		// If we cannot determine permissions, deny access.
		d.logf("could not list ACLs; denying access", "path", p, "error", err)
		return false
	}
	return hasAccess(accesses, username, required)
}

// fail logs the underlying iRODS error and returns a sanitized error so raw
// iRODS internals never reach the MCP client.
func (d *DataStore) fail(op string, err error) error {
	d.logf("iRODS operation failed", "operation", op, "error", err)
	return fmt.Errorf("data store: %s failed", op)
}

func (d *DataStore) logf(msg string, args ...any) {
	if d.logger != nil {
		d.logger.Error(msg, args...)
	}
}

func entryType(e *fs.Entry) string {
	if e.Type == fs.DirectoryEntry {
		return typeCollection
	}
	return typeDataObject
}

func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
