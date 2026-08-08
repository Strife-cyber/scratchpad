package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scratchpad/internal/protocol"

	cdpbrowser "github.com/chromedp/cdproto/browser"
)

// ---------------------------------------------------------------------------
// Download dir resolution
// ---------------------------------------------------------------------------

func TestDownloadDir_Resolution(t *testing.T) {
	t.Setenv(DownloadDirEnv, "")
	if got := resolveDownloadDir(); got != DefaultDownloadDir {
		t.Errorf("default: want %q, got %q", DefaultDownloadDir, got)
	}
	t.Setenv(DownloadDirEnv, filepath.Join("custom", "dl"))
	if got := resolveDownloadDir(); got != filepath.Join("custom", "dl") {
		t.Errorf("env: want %q, got %q", filepath.Join("custom", "dl"), got)
	}
}

func TestEngineDownloadDir_DefaultsWhenUnset(t *testing.T) {
	t.Setenv(DownloadDirEnv, "")
	e := &ChromeEngine{}
	if got := e.DownloadDir(); got != DefaultDownloadDir {
		t.Errorf("DownloadDir: want %q, got %q", DefaultDownloadDir, got)
	}
}

// ---------------------------------------------------------------------------
// Download progress state transitions (filename collision handling)
// ---------------------------------------------------------------------------

func TestApplyDownloadProgress_CollisionRename(t *testing.T) {
	info := &protocol.DownloadInfo{ID: "g3", SuggestedFilename: "report.pdf"}
	applyDownloadProgress(info, 100, 100, cdpbrowser.DownloadProgressStateCompleted, "/dl/report (1).pdf")
	if info.State != protocol.DownloadCompleted {
		t.Errorf("State: want completed, got %q", info.State)
	}
	if info.Filename != "report (1).pdf" {
		t.Errorf("Filename: want collision-renamed %q, got %q", "report (1).pdf", info.Filename)
	}
	if info.Path != "/dl/report (1).pdf" {
		t.Errorf("Path: want %q, got %q", "/dl/report (1).pdf", info.Path)
	}
	if info.ReceivedBytes != 100 || info.TotalBytes != 100 {
		t.Errorf("bytes: want 100/100, got %d/%d", info.ReceivedBytes, info.TotalBytes)
	}
}

func TestApplyDownloadProgress_Cancelled(t *testing.T) {
	info := &protocol.DownloadInfo{ID: "g4"}
	applyDownloadProgress(info, 10, 100, cdpbrowser.DownloadProgressStateCanceled, "")
	if info.State != protocol.DownloadCancelled {
		t.Errorf("State: want cancelled, got %q", info.State)
	}
	if info.Path != "" {
		t.Errorf("Path: want empty on cancel, got %q", info.Path)
	}
}

func TestApplyDownloadProgress_InProgress(t *testing.T) {
	info := &protocol.DownloadInfo{ID: "g5"}
	applyDownloadProgress(info, 50, 200, cdpbrowser.DownloadProgressStateInProgress, "")
	if info.State != protocol.DownloadInProgress {
		t.Errorf("State: want in_progress, got %q", info.State)
	}
	if info.ReceivedBytes != 50 || info.TotalBytes != 200 {
		t.Errorf("bytes: want 50/200, got %d/%d", info.ReceivedBytes, info.TotalBytes)
	}
}

// ---------------------------------------------------------------------------
// popNextDownloadGUID / waitNextDownload queue semantics
// ---------------------------------------------------------------------------

func newDownloadEngine(queue []string, downloads map[string]*protocol.DownloadInfo) *ChromeEngine {
	return &ChromeEngine{
		downloads:       downloads,
		downloadQueue:   queue,
		downloadBeginCh: make(chan struct{}),
	}
}

func TestPopNextDownloadGUID_WaitsForBegin(t *testing.T) {
	e := newDownloadEngine(nil, make(map[string]*protocol.DownloadInfo))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got := make(chan string, 1)
	go func() {
		guid, err := e.popNextDownloadGUID(ctx)
		if err != nil {
			got <- "" // signal error via empty
			return
		}
		got <- guid
	}()

	// Give the waiter a moment to block, then simulate a downloadWillBegin.
	time.Sleep(50 * time.Millisecond)
	e.downloadMu.Lock()
	e.downloads["guid-1"] = &protocol.DownloadInfo{ID: "guid-1", State: protocol.DownloadInProgress}
	e.downloadQueue = append(e.downloadQueue, "guid-1")
	close(e.downloadBeginCh)
	e.downloadBeginCh = make(chan struct{})
	e.downloadMu.Unlock()

	select {
	case g := <-got:
		if g != "guid-1" {
			t.Errorf("popNextDownloadGUID: got %q, want guid-1", g)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("popNextDownloadGUID did not return after a begin")
	}
}

func TestPopNextDownloadGUID_ConsumesInOrder(t *testing.T) {
	e := newDownloadEngine([]string{"g1", "g2"}, map[string]*protocol.DownloadInfo{
		"g1": {ID: "g1"},
		"g2": {ID: "g2"},
	})
	ctx := context.Background()
	for _, want := range []string{"g1", "g2"} {
		got, err := e.popNextDownloadGUID(ctx)
		if err != nil {
			t.Fatalf("popNextDownloadGUID: %v", err)
		}
		if got != want {
			t.Errorf("popNextDownloadGUID: got %q, want %q", got, want)
		}
	}
}

func TestPopNextDownloadGUID_Timeout(t *testing.T) {
	e := newDownloadEngine(nil, make(map[string]*protocol.DownloadInfo))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.popNextDownloadGUID(ctx); err == nil {
		t.Error("popNextDownloadGUID: want timeout error, got nil")
	}
}

func TestWaitNextDownload_AlreadyCompleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.csv")
	if err := os.WriteFile(path, []byte("a,b,c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newDownloadEngine([]string{"g1"}, map[string]*protocol.DownloadInfo{
		"g1": {
			ID: "g1", URL: "https://x/report.csv", SuggestedFilename: "report.csv",
			Filename: "report.csv", Path: path, State: protocol.DownloadCompleted,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := e.waitNextDownload(ctx)
	if err != nil {
		t.Fatalf("waitNextDownload: %v", err)
	}
	if info.Path != path {
		t.Errorf("Path: want %q, got %q", path, info.Path)
	}
	// Authoritative size re-stat: "a,b,c\n" is 6 bytes.
	if info.ReceivedBytes != 6 {
		t.Errorf("size: want 6, got %d", info.ReceivedBytes)
	}
}

func TestWaitNextDownload_WaitsForCompletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.csv")
	e := newDownloadEngine([]string{"g2"}, map[string]*protocol.DownloadInfo{
		"g2": {ID: "g2", URL: "https://x/export.csv", SuggestedFilename: "export.csv", State: protocol.DownloadInProgress},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan *protocol.DownloadInfo, 1)
	go func() {
		info, err := e.waitNextDownload(ctx)
		if err != nil {
			done <- nil
			return
		}
		done <- info
	}()

	// Let waitNextDownload see the in-progress state, then complete the file.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.downloadMu.Lock()
	applyDownloadProgress(e.downloads["g2"], 4, 4, cdpbrowser.DownloadProgressStateCompleted, path)
	e.downloadMu.Unlock()

	select {
	case info := <-done:
		if info == nil {
			t.Fatal("waitNextDownload errored")
		}
		if info.State != protocol.DownloadCompleted || info.Filename != "export.csv" {
			t.Errorf("unexpected download info: %+v", info)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitNextDownload did not return after completion")
	}
}

func TestWaitNextDownload_Timeout(t *testing.T) {
	e := newDownloadEngine(nil, make(map[string]*protocol.DownloadInfo))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.waitNextDownload(ctx); err == nil {
		t.Error("waitNextDownload: want timeout error, got nil")
	}
}

func TestDownloadMetadata(t *testing.T) {
	info := &protocol.DownloadInfo{
		ID: "g9", URL: "https://x/report.csv", SuggestedFilename: "report.csv",
		Filename: "report (1).csv", Path: "/dl/report (1).csv",
		State: protocol.DownloadCompleted, ReceivedBytes: 42, TotalBytes: 42,
	}
	meta := downloadMetadata(info)
	if meta["id"] != "g9" || meta["url"] != "https://x/report.csv" {
		t.Errorf("identity keys wrong: %+v", meta)
	}
	if meta["path"] != "/dl/report (1).csv" {
		t.Errorf("path: want final on-disk path, got %v", meta["path"])
	}
	if meta["filename"] != "report (1).csv" {
		t.Errorf("filename: want collision-renamed name, got %v", meta["filename"])
	}
	if meta["size"] != int64(42) {
		t.Errorf("size: want 42, got %v", meta["size"])
	}
	if meta["state"] != string(protocol.DownloadCompleted) {
		t.Errorf("state: want completed, got %v", meta["state"])
	}
}

func TestListDownloads_OrderedByID(t *testing.T) {
	e := &ChromeEngine{downloads: map[string]*protocol.DownloadInfo{
		"g3": {ID: "g3", State: protocol.DownloadInProgress},
		"g1": {ID: "g1", State: protocol.DownloadCompleted},
		"g2": {ID: "g2", State: protocol.DownloadCancelled},
	}}
	got := e.listDownloads()
	if len(got) != 3 {
		t.Fatalf("listDownloads: want 3 entries, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Errorf("listDownloads: not sorted by ID: %+v", got)
			break
		}
	}
}
