package mcpserver

import (
	"context"
	"encoding/base64"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apperr"
)

func (d *Deps) browseData(ctx context.Context, _ *mcp.CallToolRequest, in BrowseDataIn) (*mcp.CallToolResult, BrowseDataOut, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, BrowseDataOut{}, err
	}
	if err := d.requireData(); err != nil {
		return nil, BrowseDataOut{}, err
	}
	result, err := d.Data.Browse(id.DownstreamUsername, in.Path, in.Offset, in.Limit, in.IncludeMetadata, in.AVUDelimiter)
	if err != nil {
		return nil, BrowseDataOut{}, err
	}

	out := BrowseDataOut{
		Path:     result.Path,
		Type:     result.Type,
		Size:     result.Size,
		Offset:   result.Offset,
		Metadata: result.Metadata,
	}
	if len(result.Content) > 0 {
		out.Content = base64.StdEncoding.EncodeToString(result.Content)
	}
	if len(result.Contents) > 0 {
		out.Contents = make([]EntryOut, 0, len(result.Contents))
		for _, e := range result.Contents {
			out.Contents = append(out.Contents, EntryOut{Name: e.Name, Type: e.Type})
		}
	}
	return nil, out, nil
}

func (d *Deps) createDirectory(ctx context.Context, _ *mcp.CallToolRequest, in CreateDirectoryIn) (*mcp.CallToolResult, WriteOut, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, WriteOut{}, err
	}
	if err := d.requireData(); err != nil {
		return nil, WriteOut{}, err
	}
	result, err := d.Data.CreateDirectory(id.DownstreamUsername, in.Path, toAVUs(in.Metadata))
	if err != nil {
		return nil, WriteOut{}, err
	}
	return nil, WriteOut{Path: result.Path, Type: result.Type, Created: result.Created}, nil
}

func (d *Deps) uploadFile(ctx context.Context, _ *mcp.CallToolRequest, in UploadFileIn) (*mcp.CallToolResult, WriteOut, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, WriteOut{}, err
	}
	if err := d.requireData(); err != nil {
		return nil, WriteOut{}, err
	}
	content, err := base64.StdEncoding.DecodeString(in.Content)
	if err != nil {
		return nil, WriteOut{}, apperr.Validation("content", "content must be base64-encoded")
	}
	result, err := d.Data.UploadFile(id.DownstreamUsername, in.Path, content, toAVUs(in.Metadata), in.ReplaceMetadata)
	if err != nil {
		return nil, WriteOut{}, err
	}
	return nil, WriteOut{Path: result.Path, Type: result.Type, Created: result.Created}, nil
}

func (d *Deps) setMetadata(ctx context.Context, _ *mcp.CallToolRequest, in SetMetadataIn) (*mcp.CallToolResult, WriteOut, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, WriteOut{}, err
	}
	if err := d.requireData(); err != nil {
		return nil, WriteOut{}, err
	}
	result, err := d.Data.SetMetadata(id.DownstreamUsername, in.Path, toAVUs(in.Metadata), in.Replace)
	if err != nil {
		return nil, WriteOut{}, err
	}
	return nil, WriteOut{Path: result.Path, Type: result.Type, Created: result.Created}, nil
}

func (d *Deps) deleteData(ctx context.Context, _ *mcp.CallToolRequest, in DeleteDataIn) (*mcp.CallToolResult, DeleteDataOut, error) {
	id, err := caller(ctx)
	if err != nil {
		return nil, DeleteDataOut{}, err
	}
	if err := d.requireData(); err != nil {
		return nil, DeleteDataOut{}, err
	}
	result, err := d.Data.Delete(id.DownstreamUsername, in.Path, in.Recurse, in.DryRun)
	if err != nil {
		return nil, DeleteDataOut{}, err
	}
	return nil, DeleteDataOut{
		Path:        result.Path,
		Type:        result.Type,
		WouldDelete: result.WouldDelete,
		Deleted:     result.Deleted,
		DryRun:      result.DryRun,
		ItemCount:   result.ItemCount,
	}, nil
}
