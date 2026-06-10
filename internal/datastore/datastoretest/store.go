// Package datastoretest provides an in-memory datastore.Store for tests.
package datastoretest

import (
	"bytes"
	"errors"
	"io"
	"slices"

	"github.com/cyverse-de/formation/internal/datastore"
)

// SetMetaCall records one SetMetadata invocation.
type SetMetaCall struct {
	Path    string
	AVUs    []datastore.AVU
	Replace bool
}

// DeletedDir records one DeleteDirectory invocation.
type DeletedDir struct {
	Path    string
	Recurse bool
}

// Store is an in-memory datastore.Store. Permissions default to allowing
// the User everywhere; set DenyRead/DenyWrite to restrict specific paths.
type Store struct {
	User string

	Files map[string][]byte
	Dirs  map[string][]datastore.Entry
	Meta  map[string][]datastore.AVU

	DenyRead  []string
	DenyWrite []string
	MetaErr   error

	SetMetaCalls []SetMetaCall
	Uploads      map[string][]byte
	CreatedDirs  []string
	DeletedFiles []string
	DeletedDirs  []DeletedDir
}

// New returns an empty store that grants access to username.
func New(username string) *Store {
	return &Store{
		User:    username,
		Files:   map[string][]byte{},
		Dirs:    map[string][]datastore.Entry{},
		Meta:    map[string][]datastore.AVU{},
		Uploads: map[string][]byte{},
	}
}

func (f *Store) PathExists(p string) bool       { return f.FileExists(p) || f.CollectionExists(p) }
func (f *Store) FileExists(p string) bool       { _, ok := f.Files[p]; return ok }
func (f *Store) CollectionExists(p string) bool { _, ok := f.Dirs[p]; return ok }

func (f *Store) UserCanRead(username, p string) bool {
	return username == f.User && !slices.Contains(f.DenyRead, p)
}

func (f *Store) UserCanWrite(username, p string) bool {
	return username == f.User && !slices.Contains(f.DenyWrite, p)
}

func (f *Store) ListCollection(p string) ([]datastore.Entry, error) {
	return f.Dirs[p], nil
}

func (f *Store) CountCollectionItems(p string) (int, error) {
	return len(f.Dirs[p]), nil
}

type fakeFile struct{ *bytes.Reader }

func (fakeFile) Close() error { return nil }

func (f *Store) OpenFile(p string) (io.ReadSeekCloser, error) {
	content, ok := f.Files[p]
	if !ok {
		return nil, errors.New("no such file")
	}
	return fakeFile{bytes.NewReader(content)}, nil
}

func (f *Store) UploadFile(p string, content io.Reader) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	f.Uploads[p] = data
	f.Files[p] = data
	return nil
}

func (f *Store) CreateDirectory(p string) error {
	f.CreatedDirs = append(f.CreatedDirs, p)
	f.Dirs[p] = nil
	return nil
}

func (f *Store) Metadata(p string) ([]datastore.AVU, error) {
	if f.MetaErr != nil {
		return nil, f.MetaErr
	}
	return f.Meta[p], nil
}

func (f *Store) SetMetadata(p string, avus []datastore.AVU, replace bool) error {
	f.SetMetaCalls = append(f.SetMetaCalls, SetMetaCall{Path: p, AVUs: avus, Replace: replace})
	return nil
}

func (f *Store) DeleteFile(p string) error {
	f.DeletedFiles = append(f.DeletedFiles, p)
	delete(f.Files, p)
	return nil
}

func (f *Store) DeleteDirectory(p string, recurse bool) error {
	f.DeletedDirs = append(f.DeletedDirs, DeletedDir{Path: p, Recurse: recurse})
	delete(f.Dirs, p)
	return nil
}
