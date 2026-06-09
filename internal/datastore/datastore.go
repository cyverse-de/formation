// Package datastore wraps the CyVerse Go iRODS client to provide the
// file-and-metadata operations Formation exposes as MCP tools.
//
// Operations use proxy (impersonation) auth: the service account authenticates
// as the proxy user while the connection's client user is the authenticated
// caller, so iRODS enforces permissions natively as that user and files are
// owned by them. Connections are cached per user (see pool) and reused across
// that user's requests, keyed by the validated downstream username so one
// user's connection can never be handed to another.
package datastore

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/common"
	"github.com/cyverse/go-irodsclient/irods/types"

	"github.com/cyverse-de/formation/internal/apperr"
)

// DataStore performs iRODS operations on behalf of authenticated users via
// proxy impersonation, reusing a per-user connection from a pool.
type DataStore struct {
	pool   *pool
	logger *slog.Logger
}

// Config holds the iRODS connection parameters. User is the proxy (service)
// account that authenticates; operations act as the caller via impersonation.
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

// New returns a DataStore backed by a per-user connection pool. idleTTL bounds
// how long an idle, unreferenced connection is kept before being closed, and
// maxConns caps the number of cached connections (0 means unbounded). Call
// Close to release all connections and stop the pool.
func New(cfg Config, idleTTL time.Duration, maxConns int, logger *slog.Logger) *DataStore {
	return &DataStore{pool: newPool(cfg, idleTTL, maxConns), logger: logger}
}

// Close releases all pooled iRODS connections and stops the pool janitor.
func (d *DataStore) Close() {
	d.pool.close()
}

// Browse lists a directory or reads a file at the path, as the caller.
func (d *DataStore) Browse(username, p string, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	p = normalizePath(p)
	c, err := d.pool.acquire(username)
	if err != nil {
		return nil, d.classify(nil, "connect", username, err)
	}
	defer c.release()

	entry, err := c.fs.Stat(p)
	if err != nil {
		return nil, d.classify(c, "stat", p, err)
	}

	if entry.Type == fs.FileEntry {
		return d.readFile(c, p, entry, offset, limit, includeMetadata, avuDelimiter)
	}
	return d.listDir(c, p, includeMetadata, avuDelimiter)
}

func (d *DataStore) readFile(c *conn, p string, entry *fs.Entry, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	handle, err := c.fs.OpenFile(p, "", "r")
	if err != nil {
		return nil, d.classify(c, "open file", p, err)
	}
	defer func() { _ = handle.Close() }()

	if offset > 0 {
		if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
			return nil, d.classify(c, "seek", p, err)
		}
	}
	var reader io.Reader = handle
	if limit > 0 {
		reader = io.LimitReader(handle, int64(limit))
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, d.classify(c, "read file", p, err)
	}

	result := &BrowseResult{
		Path:    p,
		Type:    typeDataObject,
		Content: content,
		Size:    entry.Size,
		Offset:  offset,
	}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(c, p, avuDelimiter)
	}
	return result, nil
}

func (d *DataStore) listDir(c *conn, p string, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	entries, err := c.fs.List(p)
	if err != nil {
		return nil, d.classify(c, "list", p, err)
	}
	contents := make([]Entry, 0, len(entries))
	for _, e := range entries {
		contents = append(contents, Entry{Name: e.Name, Type: entryType(e)})
	}
	result := &BrowseResult{Path: p, Type: typeCollection, Contents: contents}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(c, p, avuDelimiter)
	}
	return result, nil
}

// CreateDirectory creates a collection (or sets metadata on an existing one).
func (d *DataStore) CreateDirectory(username, p string, metadata []AVU) (*WriteResult, error) {
	p = normalizePath(p)
	c, err := d.pool.acquire(username)
	if err != nil {
		return nil, d.classify(nil, "connect", username, err)
	}
	defer c.release()

	if c.fs.Exists(p) {
		if err := d.applyMetadata(c, p, metadata, false); err != nil {
			return nil, err
		}
		return &WriteResult{Path: p, Type: typeCollection, Created: false}, nil
	}

	parent := path.Dir(p)
	if !c.fs.Exists(parent) {
		return nil, apperr.NotFound("Parent directory", parent)
	}
	if err := c.fs.MakeDir(p, false); err != nil {
		return nil, d.classify(c, "make dir", p, err)
	}
	if err := d.applyMetadata(c, p, metadata, false); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeCollection, Created: true}, nil
}

// UploadFile creates or overwrites a data object with the given content.
func (d *DataStore) UploadFile(username, p string, content []byte, metadata []AVU, replaceMetadata bool) (*WriteResult, error) {
	p = normalizePath(p)
	c, err := d.pool.acquire(username)
	if err != nil {
		return nil, d.classify(nil, "connect", username, err)
	}
	defer c.release()

	entry, statErr := c.fs.Stat(p)
	switch {
	case statErr == nil:
		if entry.IsDir() {
			return nil, apperr.BadRequest("Cannot upload file - path is a directory")
		}
	case types.IsFileNotFoundError(statErr):
		parent := path.Dir(p)
		if !c.fs.Exists(parent) {
			return nil, apperr.NotFound("Parent directory", parent)
		}
	default:
		return nil, d.classify(c, "stat", p, statErr)
	}
	exists := statErr == nil

	if err := d.writeFile(c, p, content); err != nil {
		return nil, err
	}
	if err := d.applyMetadata(c, p, metadata, replaceMetadata); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeDataObject, Created: !exists}, nil
}

func (d *DataStore) writeFile(c *conn, p string, content []byte) error {
	handle, err := c.fs.CreateFile(p, "", "w")
	if err != nil {
		return d.classify(c, "create file", p, err)
	}
	if _, err := handle.Write(content); err != nil {
		_ = handle.Close()
		return d.classify(c, "write file", p, err)
	}
	if err := handle.Close(); err != nil {
		return d.classify(c, "close file", p, err)
	}
	return nil
}

// SetMetadata sets (optionally replacing) AVU metadata on an existing path.
func (d *DataStore) SetMetadata(username, p string, metadata []AVU, replace bool) (*WriteResult, error) {
	p = normalizePath(p)
	c, err := d.pool.acquire(username)
	if err != nil {
		return nil, d.classify(nil, "connect", username, err)
	}
	defer c.release()

	entry, statErr := c.fs.Stat(p)
	if statErr != nil {
		if types.IsFileNotFoundError(statErr) {
			return nil, apperr.NotFound("Path", p)
		}
		return nil, d.classify(c, "stat", p, statErr)
	}
	t := typeDataObject
	if entry.IsDir() {
		t = typeCollection
	}
	if err := d.applyMetadata(c, p, metadata, replace); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: t, Created: false}, nil
}

// Delete removes a file or directory, supporting dry-run and recursive deletion.
func (d *DataStore) Delete(username, p string, recurse, dryRun bool) (*DeleteResult, error) {
	p = normalizePath(p)
	c, err := d.pool.acquire(username)
	if err != nil {
		return nil, d.classify(nil, "connect", username, err)
	}
	defer c.release()

	entry, statErr := c.fs.Stat(p)
	if statErr != nil {
		if types.IsFileNotFoundError(statErr) {
			return nil, apperr.NotFound("Path", p)
		}
		return nil, d.classify(c, "stat", p, statErr)
	}
	if !entry.IsDir() {
		return d.deleteFile(c, p, dryRun)
	}
	return d.deleteDir(c, p, recurse, dryRun)
}

func (d *DataStore) deleteFile(c *conn, p string, dryRun bool) (*DeleteResult, error) {
	result := &DeleteResult{Path: p, Type: typeDataObject, WouldDelete: true, DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	if err := c.fs.RemoveFile(p, true); err != nil {
		return nil, d.classify(c, "remove file", p, err)
	}
	result.Deleted = true
	return result, nil
}

func (d *DataStore) deleteDir(c *conn, p string, recurse, dryRun bool) (*DeleteResult, error) {
	entries, err := c.fs.List(p)
	if err != nil {
		return nil, d.classify(c, "list", p, err)
	}
	itemCount := len(entries)

	// Apply the non-empty guard to dry-runs too, so the preview matches what the
	// real delete would do.
	if !recurse && itemCount > 0 {
		return nil, apperr.BadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
	}

	result := &DeleteResult{Path: p, Type: typeCollection, WouldDelete: true, DryRun: dryRun}
	if recurse && itemCount > 0 {
		result.ItemCount = itemCount
	}
	if dryRun {
		return result, nil
	}
	if err := c.fs.RemoveDir(p, recurse, true); err != nil {
		return nil, d.classify(c, "remove dir", p, err)
	}
	result.Deleted = true
	return result, nil
}

// applyMetadata sets AVU metadata. When replace is true, existing values for
// the attributes being set are removed first; unrelated metadata (including
// system-managed AVUs such as ipc_UUID, which the user cannot delete) is left
// intact.
func (d *DataStore) applyMetadata(c *conn, p string, metadata []AVU, replace bool) error {
	if len(metadata) == 0 {
		return nil
	}
	if replace {
		setAttrs := make(map[string]bool, len(metadata))
		for _, avu := range metadata {
			setAttrs[avu.Attribute] = true
		}
		existing, err := c.fs.ListMetadata(p)
		if err != nil {
			return d.classify(c, "list metadata", p, err)
		}
		for _, m := range existing {
			if !setAttrs[m.Name] {
				continue
			}
			if err := c.fs.DeleteMetadataByAVU(p, m.Name, m.Value, m.Units); err != nil {
				return d.classify(c, "delete metadata", p, err)
			}
		}
	}
	for _, avu := range metadata {
		if err := c.fs.AddMetadata(p, avu.Attribute, avu.Value, avu.Units); err != nil {
			return d.classify(c, "add metadata", p, err)
		}
	}
	return nil
}

func (d *DataStore) metadataHeaders(c *conn, p, delimiter string) map[string]string {
	metas, err := c.fs.ListMetadata(p)
	if err != nil {
		// Metadata is best-effort; return none on failure.
		d.logf("could not list metadata; returning none", "path", p, "error", err)
		return map[string]string{}
	}
	return formatMetadataHeaders(metas, delimiter)
}

// classify maps an iRODS error to a sanitized domain error so raw iRODS
// internals never reach the MCP client. When the failure is a transport error,
// the leased connection (if any) is discarded so the next request reconnects
// rather than reusing a dead connection.
func (d *DataStore) classify(c *conn, op, p string, err error) error {
	if c != nil && types.IsConnectionError(err) {
		c.discard()
	}
	switch {
	case types.IsFileNotFoundError(err):
		return apperr.NotFound("Path", p)
	case types.IsCollectionNotEmptyError(err):
		return apperr.BadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
	case isAccessDenied(err):
		return apperr.PermissionDenied()
	case types.IsUserNotFoundError(err):
		return errors.New("no iRODS account exists for the authenticated user")
	default:
		d.logf("iRODS operation failed", "operation", op, "path", p, "error", err)
		return fmt.Errorf("data store: %s failed", op)
	}
}

// isAccessDenied reports whether err is an iRODS permission/privilege error.
func isAccessDenied(err error) bool {
	var ie *types.IRODSError
	if !errors.As(err, &ie) {
		return false
	}
	switch ie.GetCode() {
	case common.CAT_NO_ACCESS_PERMISSION,
		common.CAT_INSUFFICIENT_PRIVILEGE_LEVEL,
		common.SYS_NO_API_PRIV:
		return true
	default:
		return false
	}
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

// normalizePath ensures an absolute, cleaned iRODS path: it resolves "..", ".",
// and redundant separators so client-supplied paths cannot express traversal,
// in addition to iRODS's own ACL enforcement.
func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}
