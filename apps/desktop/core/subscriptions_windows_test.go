package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSubscriptionStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	const link = "trojan://synthetic-password@node.invalid:443?security=tls#Test"
	var version atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version.Load() == 2 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100; expire=1900000000")
		fmt.Fprint(w, link)
		if version.Load() == 1 {
			fmt.Fprint(w, "\n"+link+"2")
		}
	}))
	defer server.Close()
	sourceURL := server.URL + "/synthetic-feed-token"
	views, err := manageSubscriptions("import_subscription", sourceURL, "")
	if err != nil || len(views) != 1 || len(views[0].Nodes) != 1 {
		t.Fatalf("import failed: %v", err)
	}
	view := views[0]
	if view.Remaining == nil || *view.Remaining != 70 || view.Expire == nil || *view.Expire != 1900000000 {
		t.Fatal("missing subscription allowance")
	}
	path := filepath.Join(dir, "Nexus", "desktop", "subscriptions", view.ID+".dpapi")
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"synthetic-feed-token", "synthetic-password", "node.invalid"} {
		if bytes.Contains(ciphertext, []byte(marker)) || bytes.Contains(public, []byte(marker)) {
			t.Fatal("subscription secret appeared outside DPAPI")
		}
	}
	version.Store(1)
	views, err = manageSubscriptions("update_subscription", "", view.ID)
	if err != nil || len(views) != 1 || len(views[0].Nodes) != 2 || views[0].Nodes[0].ID != view.Nodes[0].ID {
		t.Fatal("refresh lost the selected node")
	}
	version.Store(2)
	if _, err := manageSubscriptions("update_subscription", "", view.ID); err != errSubDownload {
		t.Fatal("missing download error")
	}
	if node, err := subscriptionNode(view.ID, view.Nodes[0].ID); err != nil || node.Name != "Test" {
		t.Fatal("cached node unavailable after failed refresh")
	}
	views, err = manageSubscriptions("remove_subscription", "", view.ID)
	if err != nil || len(views) != 0 {
		t.Fatal("delete failed")
	}
	if _, err := subscriptionNode(view.ID, view.Nodes[0].ID); err != errSubChanged {
		t.Fatal("deleted node still selectable")
	}
}
