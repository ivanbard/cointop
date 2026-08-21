package cmd

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSubprocessDiscoveryAndStdoutPurity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the cointop subprocess")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "cointop")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/cointop")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build subprocess: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "subprocess-test", Version: "vtest"}, nil)
	command := exec.Command(binary, "mcp", "--cache-dir", t.TempDir())
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("stdio initialization failed (stdout must contain protocol messages only): %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 7 {
		names := make([]string, 0, len(tools.Tools))
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("got %d tools: %v", len(tools.Tools), names)
	}
}
