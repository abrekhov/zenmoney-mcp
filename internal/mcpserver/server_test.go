package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/abrekhov/zenmoney-mcp/internal/zenmoney"
)

type fakeAPI struct{}

func (*fakeAPI) Diff(context.Context, zenmoney.DiffRequest) (zenmoney.DiffResponse, error) {
	return zenmoney.DiffResponse{}, nil
}
func (*fakeAPI) Suggest(context.Context, []zenmoney.SuggestRequest) ([]zenmoney.SuggestResponse, error) {
	return nil, nil
}

func TestToolSurfaceContainsNoDeleteOrRemoveTool(t *testing.T) {
	ctx := context.Background()
	api := &fakeAPI{}
	server := New(api, zenmoney.NewState(api))
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 9 {
		t.Fatalf("expected 9 tools, got %d", len(result.Tools))
	}
	for _, tool := range result.Tools {
		name := strings.ToLower(tool.Name)
		if strings.Contains(name, "delete") || strings.Contains(name, "remove") || strings.Contains(name, "archive") {
			t.Fatalf("forbidden destructive tool exposed: %s", tool.Name)
		}
	}
}
