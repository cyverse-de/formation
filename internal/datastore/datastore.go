// Package datastore wraps iRODS access for the /data endpoints. Like the
// Python version it uses a single rodsadmin identity with app-level ACL
// checks rather than proxying as the requesting user.
package datastore

import (
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"sync"

	irodsfs "github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/types"
)

// Entry types reported in directory listings and delete results.
const (
	TypeCollection = "collection"
	TypeDataObject = "data_object"
)

// Entry is one item in a collection listing.
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// AVU is one iRODS attribute-value-units metadata triple.
type AVU struct {
	Attribute string
	Value     string
	Units     string
}

// Store is the narrow iRODS surface the /data handlers use; handler tests
// substitute a fake.
type Store interface {
	PathExists(irodsPath string) bool
	FileExists(irodsPath string) bool
	CollectionExists(irodsPath string) bool
	UserCanRead(username, irodsPath string) bool
	UserCanWrite(username, irodsPath string) bool
	ListCollection(irodsPath string) ([]Entry, error)
	CountCollectionItems(irodsPath string) (int, error)
	OpenFile(irodsPath string) (io.ReadSeekCloser, error)
	UploadFile(irodsPath string, content io.Reader) error
	CreateDirectory(irodsPath string) error
	Metadata(irodsPath string) ([]AVU, error)
	SetMetadata(irodsPath string, avus []AVU, replace bool) error
	DeleteFile(irodsPath string) error
	DeleteDirectory(irodsPath string, recurse bool) error
}

// readAccessLevels and writeAccessLevels are the buckets the Python version
// used for its app-level authorization checks ({read, write, own} and
// {write, own} respectively), in go-irodsclient's 4.3-style level names.
var (
	readAccessLevels  = []types.IRODSAccessLevelType{types.IRODSAccessLevelReadObject, types.IRODSAccessLevelModifyObject, types.IRODSAccessLevelOwner}
	writeAccessLevels = []types.IRODSAccessLevelType{types.IRODSAccessLevelModifyObject, types.IRODSAccessLevelOwner}
)

// IRODS is the real Store backed by a go-irodsclient FileSystem and its
// built-in connection pool. The connection is established lazily on first
// use so the service starts without a reachable iRODS, like the Python
// version's lazy session.
type IRODS struct {
	account *types.IRODSAccount

	mu sync.Mutex
	fs *irodsfs.FileSystem
}

// NewIRODS prepares an iRODS store for the configured (rodsadmin) account.
func NewIRODS(host, port, user, password, zone string) (*IRODS, error) {
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("invalid iRODS port %q: %w", port, err)
	}
	account, err := types.CreateIRODSAccount(host, portNum, user, zone, types.AuthSchemeNative, password, "")
	if err != nil {
		return nil, fmt.Errorf("creating iRODS account: %w", err)
	}
	return &IRODS{account: account}, nil
}

// filesystem returns the connected FileSystem, dialing iRODS on first use.
func (s *IRODS) filesystem() (*irodsfs.FileSystem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fs == nil {
		filesystem, err := irodsfs.NewFileSystemWithDefault(s.account, "formation")
		if err != nil {
			return nil, fmt.Errorf("connecting to iRODS: %w", err)
		}
		s.fs = filesystem
	}
	return s.fs, nil
}

// Release closes the iRODS connection pool.
func (s *IRODS) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fs != nil {
		s.fs.Release()
		s.fs = nil
	}
}

// PathExists reports whether a data object or collection exists at the path.
func (s *IRODS) PathExists(irodsPath string) bool {
	filesystem, err := s.filesystem()
	return err == nil && filesystem.Exists(irodsPath)
}

// FileExists reports whether a data object exists at the path.
func (s *IRODS) FileExists(irodsPath string) bool {
	filesystem, err := s.filesystem()
	return err == nil && filesystem.ExistsFile(irodsPath)
}

// CollectionExists reports whether a collection exists at the path.
func (s *IRODS) CollectionExists(irodsPath string) bool {
	filesystem, err := s.filesystem()
	return err == nil && filesystem.ExistsDir(irodsPath)
}

// UserCanRead reports whether the user holds read, write, or own on the path.
func (s *IRODS) UserCanRead(username, irodsPath string) bool {
	return s.userHasAccess(username, irodsPath, readAccessLevels)
}

// UserCanWrite reports whether the user holds write or own on the path.
func (s *IRODS) UserCanWrite(username, irodsPath string) bool {
	return s.userHasAccess(username, irodsPath, writeAccessLevels)
}

// userHasAccess checks the path's ACL for the user, denying on lookup errors
// like the Python version. Only direct user grants count; group membership is
// not expanded (matching the original behavior).
func (s *IRODS) userHasAccess(username, irodsPath string, levels []types.IRODSAccessLevelType) bool {
	filesystem, err := s.filesystem()
	if err != nil {
		return false
	}
	accesses, err := filesystem.ListACLs(irodsPath)
	if err != nil {
		return false
	}
	for _, access := range accesses {
		if access.UserName == username && slices.Contains(levels, access.AccessLevel) {
			return true
		}
	}
	return false
}

// ListCollection lists a collection's immediate children, subcollections
// first, matching the Python listing order.
func (s *IRODS) ListCollection(irodsPath string) ([]Entry, error) {
	filesystem, err := s.filesystem()
	if err != nil {
		return nil, err
	}
	children, err := filesystem.List(irodsPath)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		if child.IsDir() {
			entries = append(entries, Entry{Name: child.Name, Type: TypeCollection})
		}
	}
	for _, child := range children {
		if !child.IsDir() {
			entries = append(entries, Entry{Name: child.Name, Type: TypeDataObject})
		}
	}
	return entries, nil
}

// CountCollectionItems counts a collection's immediate children.
func (s *IRODS) CountCollectionItems(irodsPath string) (int, error) {
	filesystem, err := s.filesystem()
	if err != nil {
		return 0, err
	}
	children, err := filesystem.List(irodsPath)
	if err != nil {
		return 0, err
	}
	return len(children), nil
}

// OpenFile opens a data object for reading.
func (s *IRODS) OpenFile(irodsPath string) (io.ReadSeekCloser, error) {
	filesystem, err := s.filesystem()
	if err != nil {
		return nil, err
	}
	return filesystem.OpenFile(irodsPath, "", string(types.FileOpenModeReadOnly))
}

// UploadFile streams content into a data object, replacing any existing
// content. The parent collection is created one level deep if missing, like
// the Python upload helper.
func (s *IRODS) UploadFile(irodsPath string, content io.Reader) error {
	filesystem, err := s.filesystem()
	if err != nil {
		return err
	}
	parent := path.Dir(irodsPath)
	if !filesystem.ExistsDir(parent) {
		if err := filesystem.MakeDir(parent, false); err != nil {
			return err
		}
	}

	// "w+" creates the data object if needed and truncates existing content.
	handle, err := filesystem.OpenFile(irodsPath, "", string(types.FileOpenModeWriteTruncate))
	if err != nil {
		return err
	}
	if _, err := io.Copy(handle, content); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}

// CreateDirectory creates a collection (non-recursively, like the Python version).
func (s *IRODS) CreateDirectory(irodsPath string) error {
	filesystem, err := s.filesystem()
	if err != nil {
		return err
	}
	return filesystem.MakeDir(irodsPath, false)
}

// Metadata returns the AVU metadata on a data object or collection.
func (s *IRODS) Metadata(irodsPath string) ([]AVU, error) {
	filesystem, err := s.filesystem()
	if err != nil {
		return nil, err
	}
	metas, err := filesystem.ListMetadata(irodsPath)
	if err != nil {
		return nil, err
	}
	avus := make([]AVU, 0, len(metas))
	for _, meta := range metas {
		avus = append(avus, AVU{Attribute: meta.Name, Value: meta.Value, Units: meta.Units})
	}
	return avus, nil
}

// SetMetadata adds AVUs to a path. With replace, only the attributes being
// set are cleared first, preserving unrelated AVUs such as ipc_UUID (a
// deliberate improvement over the Python version, which cleared everything).
func (s *IRODS) SetMetadata(irodsPath string, avus []AVU, replace bool) error {
	filesystem, err := s.filesystem()
	if err != nil {
		return err
	}
	if replace {
		// Only clear attributes that currently exist: the wildcard metadata
		// delete errors on attributes with no AVUs.
		existing, err := filesystem.ListMetadata(irodsPath)
		if err != nil {
			return err
		}
		present := make(map[string]bool, len(existing))
		for _, meta := range existing {
			present[meta.Name] = true
		}
		cleared := make(map[string]bool, len(avus))
		for _, avu := range avus {
			if !present[avu.Attribute] || cleared[avu.Attribute] {
				continue
			}
			cleared[avu.Attribute] = true
			if err := filesystem.DeleteMetadataByName(irodsPath, avu.Attribute); err != nil {
				return err
			}
		}
	}
	for _, avu := range avus {
		if err := filesystem.AddMetadata(irodsPath, avu.Attribute, avu.Value, avu.Units); err != nil {
			return err
		}
	}
	return nil
}

// DeleteFile removes a data object. Like the Python unlink call it does not
// force, so the object lands in the trash when trash is enabled.
func (s *IRODS) DeleteFile(irodsPath string) error {
	filesystem, err := s.filesystem()
	if err != nil {
		return err
	}
	return filesystem.RemoveFile(irodsPath, false)
}

// DeleteDirectory removes a collection, bypassing the trash (force) like the
// Python collections.remove call.
func (s *IRODS) DeleteDirectory(irodsPath string, recurse bool) error {
	filesystem, err := s.filesystem()
	if err != nil {
		return err
	}
	return filesystem.RemoveDir(irodsPath, recurse, true)
}
