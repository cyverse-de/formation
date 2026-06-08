package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewRegistersAllTools(t *testing.T) {
	srv := New(&Deps{Version: "test"})

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	result, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range result.Tools {
		got[tool.Name] = true
	}

	want := []string{
		"list_apps", "get_app_parameters", "launch_app_and_wait", "get_analysis_status",
		"list_running_analyses", "stop_analysis", "open_in_browser",
		"browse_data", "create_directory", "upload_file", "delete_data", "set_metadata",
	}
	if len(got) != len(want) {
		t.Errorf("registered %d tools, want %d: %v", len(got), len(want), got)
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("tool %q not registered", name)
		}
	}
}
