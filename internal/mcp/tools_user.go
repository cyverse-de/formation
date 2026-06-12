package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type whoamiInput struct{}

func (s *server) registerUserTools(srv *sdk.Server) {
	sdk.AddTool(srv, &sdk.Tool{
		Name: "whoami",
		Description: "Get the authenticated user's account info and data-store " +
			"locations: username, full username, name, email, home directory, " +
			"trash directory, and default analysis output folder. Call this first " +
			"to learn the correct iRODS paths before browsing or uploading data.",
	}, wrapTool("whoami", s.whoami))
}

func (s *server) whoami(ctx context.Context, req *sdk.CallToolRequest, _ whoamiInput) (*sdk.CallToolResult, error) {
	claims, token, err := requestIdentity(req)
	if err != nil {
		return nil, err
	}
	info, err := s.user.Info(ctx, token, claims)
	if err != nil {
		return nil, err
	}
	return textResult(formatWhoami(info)), nil
}
