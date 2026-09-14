package codex

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestArchivedThreadCatalogPaginatesExplicitArchiveFilter(t *testing.T) {
	t.Setenv("KSFA_DRAFT_RPC_FIXTURE", "1")
	t.Setenv("KSFA_ARCHIVE_RPC_FIXTURE", "1")
	t.Setenv("KSFA_DRAFT_RPC_LOG", t.TempDir()+"/rpc.jsonl")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Executable: binary, Timeout: time.Second}
	defer c.Close()
	ids, err := c.FetchArchivedThreadIDs(context.Background())
	if err != nil || len(ids) != 2 || ids[0] != "archived-1" || ids[1] != "archived-2" {
		t.Fatalf("%v %v", ids, err)
	}
}
