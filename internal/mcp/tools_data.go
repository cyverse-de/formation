package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/clients"
)

type browseDataInput struct {
	Path            string `json:"path" jsonschema:"Full iRODS path to browse (e.g., '/<zone>/home/username/directory' or '/<zone>/home/username/file.txt'); call whoami to learn your home directory path"`
	Offset          int    `json:"offset,omitempty" jsonschema:"Byte offset for file reading (default: 0)"`
	Limit           int    `json:"limit,omitempty" jsonschema:"Max bytes to read for files (optional)"`
	IncludeMetadata bool   `json:"include_metadata,omitempty" jsonschema:"Include iRODS AVU metadata (default: false)"`
}

type createDirectoryInput struct {
	Path     string            `json:"path" jsonschema:"Full iRODS path for the new directory (e.g., '/<zone>/home/username/newdir')"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"Optional metadata as key-value pairs (e.g., {'author': 'username', 'project': 'myproject'})"`
}

type uploadFileInput struct {
	Path     string            `json:"path" jsonschema:"Full iRODS path for the file (e.g., '/<zone>/home/username/file.txt')"`
	Content  string            `json:"content" jsonschema:"File content as a string"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"Optional metadata as key-value pairs (e.g., {'author': 'username', 'filetype': 'text'})"`
}

type setMetadataInput struct {
	Path     string            `json:"path" jsonschema:"Full iRODS path to file or directory (e.g., '/<zone>/home/username/file.txt')"`
	Metadata map[string]string `json:"metadata" jsonschema:"Metadata as key-value pairs (e.g., {'author': 'username', 'version': '1.0'})"`
	Replace  bool              `json:"replace,omitempty" jsonschema:"If true, replace all existing metadata. If false, add to existing metadata (default: false)"`
}

type deleteDataInput struct {
	Path    string `json:"path" jsonschema:"Full iRODS path to the file or directory to delete"`
	Recurse bool   `json:"recurse,omitempty" jsonschema:"Required to delete non-empty directories (default: false)"`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"Preview the deletion without executing it (default: false)"`
}

func (s *server) registerDataTools(srv *sdk.Server) {
	sdk.AddTool(srv, &sdk.Tool{
		Name: "browse_data",
		Description: "Browse iRODS data store directory or read file contents. " +
			"For directories, returns a list of contents. " +
			"For files, returns the file content.",
	}, wrapTool("browse_data", s.browseData))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "create_directory",
		Description: "Create a new directory in the iRODS data store",
	}, wrapTool("create_directory", s.createDirectory))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "upload_file",
		Description: "Upload a file to the iRODS data store",
	}, wrapTool("upload_file", s.uploadFile))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "set_metadata",
		Description: "Set or update metadata on an existing file or directory",
	}, wrapTool("set_metadata", s.setMetadata))

	sdk.AddTool(srv, &sdk.Tool{
		Name: "delete_data",
		Description: "Delete a file or directory from the iRODS data store. " +
			"Supports dry-run mode to preview deletion without executing. " +
			"**Safety:** Deletions are permanent. Consider using dry_run=true first.",
	}, wrapTool("delete_data", s.deleteData))
}

// irodsPath normalizes a tool-supplied path to have a leading slash.
func irodsPath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// avusFromMap converts tool metadata to AVUs the same way the former REST
// API read X-Datastore-* headers: attribute names lowercased, values split
// into value and units on the first comma, sorted by attribute.
func avusFromMap(metadata map[string]string) []clients.MetadataAVU {
	avus := make([]clients.MetadataAVU, 0, len(metadata))
	for attribute, value := range metadata {
		units := ""
		if split := strings.SplitN(value, ",", 2); len(split) == 2 {
			value, units = split[0], split[1]
		}
		avus = append(avus, clients.MetadataAVU{Attr: strings.ToLower(attribute), Value: value, Unit: units})
	}
	slices.SortFunc(avus, func(a, b clients.MetadataAVU) int { return strings.Compare(a.Attr, b.Attr) })
	return avus
}

func (s *server) browseData(ctx context.Context, req *sdk.CallToolRequest, in browseDataInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	result, err := s.data.Browse(ctx, caller.Token, irodsPath(in.Path), in.Offset, in.Limit, in.IncludeMetadata, s.cfg.MCPBrowseByteLimit)
	if err != nil {
		return nil, err
	}
	return textResult(formatBrowse(result)), nil
}

func (s *server) createDirectory(ctx context.Context, req *sdk.CallToolRequest, in createDirectoryInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	result, err := s.data.MakeDirectory(ctx, caller.Token, irodsPath(in.Path), avusFromMap(in.Metadata), false)
	if err != nil {
		return nil, err
	}
	return textResult(fmt.Sprintf("Directory created: `%s`", strOr(result, "path", ""))), nil
}

func (s *server) uploadFile(ctx context.Context, req *sdk.CallToolRequest, in uploadFileInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	result, err := s.data.UploadFile(ctx, caller.Token, irodsPath(in.Path), strings.NewReader(in.Content), avusFromMap(in.Metadata), false)
	if err != nil {
		return nil, err
	}

	action := "updated"
	if created, _ := result["created"].(bool); created {
		action = "created"
	}
	return textResult(fmt.Sprintf("File %s: `%s`", action, strOr(result, "path", ""))), nil
}

func (s *server) setMetadata(ctx context.Context, req *sdk.CallToolRequest, in setMetadataInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	result, err := s.data.UpdateMetadata(ctx, caller.Token, irodsPath(in.Path), avusFromMap(in.Metadata), in.Replace)
	if err != nil {
		return nil, err
	}

	action := "updated"
	if in.Replace {
		action = "replaced"
	}
	return textResult(fmt.Sprintf("Metadata %s for: `%s`", action, strOr(result, "path", ""))), nil
}

func (s *server) deleteData(ctx context.Context, req *sdk.CallToolRequest, in deleteDataInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	result, err := s.data.DeletePath(ctx, caller.Token, irodsPath(in.Path), in.Recurse, in.DryRun)
	if err != nil {
		return nil, err
	}
	return textResult(formatDelete(result, in.Recurse)), nil
}
