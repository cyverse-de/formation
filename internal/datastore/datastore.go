// Package datastore wraps the CyVerse Go iRODS client to provide the
// file-and-metadata operations Formation exposes as MCP tools.
//
// Each operation connects to iRODS using proxy (impersonation) auth: the
// service account authenticates as the proxy user while the connection's client
// user is the authenticated caller, so iRODS enforces permissions natively as
// that user and files are owned by them. A fresh connection is opened per
// operation and released when it completes.
package datastore

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/common"
	"github.com/cyverse/go-irodsclient/irods/types"

	"github.com/cyverse-de/formation/internal/apperr"
)

// DataStore performs iRODS operations on behalf of authenticated users via
// proxy impersonation.
type DataStore struct {
	host      string
	port      int
	proxyUser string
	password  string
	zone      string
	logger    *slog.Logger
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

// New returns a DataStore. It does not open a connection; connections are
// established per operation against the impersonated caller.
func New(cfg Config, logger *slog.Logger) *DataStore {
	return &DataStore{
		host:      cfg.Host,
		port:      cfg.Port,
		proxyUser: cfg.User,
		password:  cfg.Password,
		zone:      cfg.Zone,
		logger:    logger,
	}
}

// connect opens an iRODS connection that authenticates as the proxy user but
// acts as the given client user. The caller must Release the result.
func (d *DataStore) connect(username string) (*fs.FileSystem, error) {
	account, err := types.CreateIRODSProxyAccount(
		d.host, d.port,
		username, d.zone, // client user / zone
		d.proxyUser, d.zone, // proxy user / zone
		types.AuthSchemeNative, d.password, "",
	)
	if err != nil {
		return nil, fmt.Errorf("data store: invalid account: %w", err)
	}
	filesystem, err := fs.NewFileSystemWithDefault(account, "formation")
	if err != nil {
		return nil, d.classify("connect", username, err)
	}
	return filesystem, nil
}

// Browse lists a directory or reads a file at the path, as the caller.
func (d *DataStore) Browse(username, p string, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	p = normalizePath(p)
	fsys, err := d.connect(username)
	if err != nil {
		return nil, err
	}
	defer fsys.Release()

	entry, err := fsys.Stat(p)
	if err != nil {
		return nil, d.classify("stat", p, err)
	}

	if entry.Type == fs.FileEntry {
		return d.readFile(fsys, p, entry, offset, limit, includeMetadata, avuDelimiter)
	}
	return d.listDir(fsys, p, includeMetadata, avuDelimiter)
}

func (d *DataStore) readFile(fsys *fs.FileSystem, p string, entry *fs.Entry, offset, limit int, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	handle, err := fsys.OpenFile(p, "", "r")
	if err != nil {
		return nil, d.classify("open file", p, err)
	}
	defer func() { _ = handle.Close() }()

	if offset > 0 {
		if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
			return nil, d.classify("seek", p, err)
		}
	}
	var reader io.Reader = handle
	if limit > 0 {
		reader = io.LimitReader(handle, int64(limit))
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, d.classify("read file", p, err)
	}

	result := &BrowseResult{
		Path:    p,
		Type:    typeDataObject,
		Content: content,
		Size:    entry.Size,
		Offset:  offset,
	}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(fsys, p, avuDelimiter)
	}
	return result, nil
}

func (d *DataStore) listDir(fsys *fs.FileSystem, p string, includeMetadata bool, avuDelimiter string) (*BrowseResult, error) {
	entries, err := fsys.List(p)
	if err != nil {
		return nil, d.classify("list", p, err)
	}
	contents := make([]Entry, 0, len(entries))
	for _, e := range entries {
		contents = append(contents, Entry{Name: e.Name, Type: entryType(e)})
	}
	result := &BrowseResult{Path: p, Type: typeCollection, Contents: contents}
	if includeMetadata {
		result.Metadata = d.metadataHeaders(fsys, p, avuDelimiter)
	}
	return result, nil
}

// CreateDirectory creates a collection (or sets metadata on an existing one).
func (d *DataStore) CreateDirectory(username, p string, metadata []AVU) (*WriteResult, error) {
	p = normalizePath(p)
	fsys, err := d.connect(username)
	if err != nil {
		return nil, err
	}
	defer fsys.Release()

	if fsys.Exists(p) {
		if err := d.applyMetadata(fsys, p, metadata, false); err != nil {
			return nil, err
		}
		return &WriteResult{Path: p, Type: typeCollection, Created: false}, nil
	}

	parent := path.Dir(p)
	if !fsys.Exists(parent) {
		return nil, apperr.NotFound("Parent directory", parent)
	}
	if err := fsys.MakeDir(p, false); err != nil {
		return nil, d.classify("make dir", p, err)
	}
	if err := d.applyMetadata(fsys, p, metadata, false); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeCollection, Created: true}, nil
}

// UploadFile creates or overwrites a data object with the given content.
func (d *DataStore) UploadFile(username, p string, content []byte, metadata []AVU, replaceMetadata bool) (*WriteResult, error) {
	p = normalizePath(p)
	fsys, err := d.connect(username)
	if err != nil {
		return nil, err
	}
	defer fsys.Release()

	exists := fsys.Exists(p)
	if exists && fsys.ExistsDir(p) {
		return nil, apperr.BadRequest("Cannot upload file - path is a directory")
	}
	if !exists {
		parent := path.Dir(p)
		if !fsys.Exists(parent) {
			return nil, apperr.NotFound("Parent directory", parent)
		}
	}

	if err := d.writeFile(fsys, p, content); err != nil {
		return nil, err
	}
	if err := d.applyMetadata(fsys, p, metadata, replaceMetadata); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: typeDataObject, Created: !exists}, nil
}

func (d *DataStore) writeFile(fsys *fs.FileSystem, p string, content []byte) error {
	handle, err := fsys.CreateFile(p, "", "w")
	if err != nil {
		return d.classify("create file", p, err)
	}
	if _, err := handle.Write(content); err != nil {
		_ = handle.Close()
		return d.classify("write file", p, err)
	}
	if err := handle.Close(); err != nil {
		return d.classify("close file", p, err)
	}
	return nil
}

// SetMetadata sets (optionally replacing) AVU metadata on an existing path.
func (d *DataStore) SetMetadata(username, p string, metadata []AVU, replace bool) (*WriteResult, error) {
	p = normalizePath(p)
	fsys, err := d.connect(username)
	if err != nil {
		return nil, err
	}
	defer fsys.Release()

	if !fsys.Exists(p) {
		return nil, apperr.NotFound("Path", p)
	}
	t := typeDataObject
	if fsys.ExistsDir(p) {
		t = typeCollection
	}
	if err := d.applyMetadata(fsys, p, metadata, replace); err != nil {
		return nil, err
	}
	return &WriteResult{Path: p, Type: t, Created: false}, nil
}

// Delete removes a file or directory, supporting dry-run and recursive deletion.
func (d *DataStore) Delete(username, p string, recurse, dryRun bool) (*DeleteResult, error) {
	p = normalizePath(p)
	fsys, err := d.connect(username)
	if err != nil {
		return nil, err
	}
	defer fsys.Release()

	if !fsys.Exists(p) {
		return nil, apperr.NotFound("Path", p)
	}
	if fsys.ExistsFile(p) {
		return d.deleteFile(fsys, p, dryRun)
	}
	return d.deleteDir(fsys, p, recurse, dryRun)
}

func (d *DataStore) deleteFile(fsys *fs.FileSystem, p string, dryRun bool) (*DeleteResult, error) {
	result := &DeleteResult{Path: p, Type: typeDataObject, WouldDelete: true, DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	if err := fsys.RemoveFile(p, true); err != nil {
		return nil, d.classify("remove file", p, err)
	}
	result.Deleted = true
	return result, nil
}

func (d *DataStore) deleteDir(fsys *fs.FileSystem, p string, recurse, dryRun bool) (*DeleteResult, error) {
	entries, err := fsys.List(p)
	if err != nil {
		return nil, d.classify("list", p, err)
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
	if err := fsys.RemoveDir(p, recurse, true); err != nil {
		return nil, d.classify("remove dir", p, err)
	}
	result.Deleted = true
	return result, nil
}

// applyMetadata sets AVU metadata. When replace is true, existing values for
// the attributes being set are removed first; unrelated metadata (including
// system-managed AVUs such as ipc_UUID, which the user cannot delete) is left
// intact.
func (d *DataStore) applyMetadata(fsys *fs.FileSystem, p string, metadata []AVU, replace bool) error {
	if len(metadata) == 0 {
		return nil
	}
	if replace {
		setAttrs := make(map[string]bool, len(metadata))
		for _, avu := range metadata {
			setAttrs[avu.Attribute] = true
		}
		existing, err := fsys.ListMetadata(p)
		if err != nil {
			return d.classify("list metadata", p, err)
		}
		for _, m := range existing {
			if !setAttrs[m.Name] {
				continue
			}
			if err := fsys.DeleteMetadataByAVU(p, m.Name, m.Value, m.Units); err != nil {
				return d.classify("delete metadata", p, err)
			}
		}
	}
	for _, avu := range metadata {
		if err := fsys.AddMetadata(p, avu.Attribute, avu.Value, avu.Units); err != nil {
			return d.classify("add metadata", p, err)
		}
	}
	return nil
}

func (d *DataStore) metadataHeaders(fsys *fs.FileSystem, p, delimiter string) map[string]string {
	metas, err := fsys.ListMetadata(p)
	if err != nil {
		// Metadata is best-effort; return none on failure.
		d.logf("could not list metadata; returning none", "path", p, "error", err)
		return map[string]string{}
	}
	return formatMetadataHeaders(metas, delimiter)
}

// classify maps an iRODS error to a sanitized domain error so raw iRODS
// internals never reach the MCP client.
func (d *DataStore) classify(op, p string, err error) error {
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

func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
