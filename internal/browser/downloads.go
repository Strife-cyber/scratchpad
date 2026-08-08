package browser

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"scratchpad/internal/protocol"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// DownloadDirEnv names the environment variable selecting the per-session
// download directory (improvement-plan item 17).
const DownloadDirEnv = "SCRATCHPAD_DOWNLOAD_DIR"

// DefaultDownloadDir is used when SCRATCHPAD_DOWNLOAD_DIR is not set.
const DefaultDownloadDir = "downloads"

// resolveDownloadDir returns the configured download directory, falling back to
// DefaultDownloadDir when SCRATCHPAD_DOWNLOAD_DIR is unset or empty.
func resolveDownloadDir() string {
	if d := os.Getenv(DownloadDirEnv); d != "" {
		return d
	}
	return DefaultDownloadDir
}

// setupDownloadBehavior enables CDP download handling for the session: downloads
// are allowed, saved under the session's download dir, and their lifecycle
// events (downloadWillBegin/downloadProgress) are emitted. Best-effort — a
// failure only degrades download tracking, never the session.
func (e *ChromeEngine) setupDownloadBehavior() {
	e.downloadMu.Lock()
	dir := e.downloadDir
	e.downloadMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("browser: download dir", "dir", dir, "err", err)
	}
	if err := chromedp.Run(e.ctx,
		cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(dir).
			WithEventsEnabled(true),
	); err != nil {
		slog.Warn("browser: set download behavior", "err", err)
	}
}

// setupDownloadListener folds the CDP download events into the session's
// download table and notifies waiters (wait_download). It is re-registered for
// each tab context by SwitchTab.
func (e *ChromeEngine) setupDownloadListener() {
	chromedp.ListenTarget(e.ctx, func(ev any) {
		switch ev2 := ev.(type) {
		case *cdpbrowser.EventDownloadWillBegin:
			e.downloadMu.Lock()
			e.downloads[ev2.GUID] = &protocol.DownloadInfo{
				ID:                ev2.GUID,
				URL:               ev2.URL,
				SuggestedFilename: ev2.SuggestedFilename,
				State:             protocol.DownloadInProgress,
			}
			e.downloadQueue = append(e.downloadQueue, ev2.GUID)
			// Wake wait_download callers: close the current begin channel and
			// replace it with a fresh one (classic "broadcast wakeup" pattern).
			close(e.downloadBeginCh)
			e.downloadBeginCh = make(chan struct{})
			e.downloadMu.Unlock()

		case *cdpbrowser.EventDownloadProgress:
			e.downloadMu.Lock()
			info, ok := e.downloads[ev2.GUID]
			if !ok {
				// Progress can precede willBegin in rare timing windows.
				info = &protocol.DownloadInfo{ID: ev2.GUID, State: protocol.DownloadInProgress}
				e.downloads[ev2.GUID] = info
			}
			applyDownloadProgress(info, ev2.ReceivedBytes, ev2.TotalBytes, ev2.State, ev2.FilePath)
			e.downloadMu.Unlock()
		}
	})
}

// applyDownloadProgress folds one CDP downloadProgress event into info. It is a
// package-level pure helper so the state transitions (including Chrome's
// collision renaming via filePath) are unit-testable without a live browser.
func applyDownloadProgress(info *protocol.DownloadInfo, received, total float64, state cdpbrowser.DownloadProgressState, filePath string) {
	info.ReceivedBytes = int64(received)
	info.TotalBytes = int64(total)
	switch state {
	case cdpbrowser.DownloadProgressStateCompleted:
		info.State = protocol.DownloadCompleted
		// Chrome names colliding files "name (1).ext", so the final on-disk
		// name comes from the event's filePath, not the suggested filename.
		if filePath != "" {
			info.Path = filePath
			info.Filename = filepath.Base(filePath)
		}
	case cdpbrowser.DownloadProgressStateCanceled:
		info.State = protocol.DownloadCancelled
	default:
		info.State = protocol.DownloadInProgress
	}
}

// DownloadDir returns the session's download directory, creating it on first
// call. Exposed via PageInfo.Extra so agents know where exported files land.
func (e *ChromeEngine) DownloadDir() string {
	e.downloadMu.Lock()
	defer e.downloadMu.Unlock()
	if e.downloadDir == "" {
		e.downloadDir = resolveDownloadDir()
	}
	return e.downloadDir
}

// listDownloads returns a snapshot of every download seen by the session,
// ordered by download GUID for determinism.
func (e *ChromeEngine) listDownloads() []protocol.DownloadInfo {
	e.downloadMu.Lock()
	defer e.downloadMu.Unlock()
	out := make([]protocol.DownloadInfo, 0, len(e.downloads))
	for _, d := range e.downloads {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// popNextDownloadGUID consumes the next began download from the FIFO queue,
// waiting (up to ctx's deadline) for one to begin if the queue is empty.
func (e *ChromeEngine) popNextDownloadGUID(ctx context.Context) (string, error) {
	for {
		e.downloadMu.Lock()
		if len(e.downloadQueue) > 0 {
			guid := e.downloadQueue[0]
			e.downloadQueue = e.downloadQueue[1:]
			e.downloadMu.Unlock()
			return guid, nil
		}
		ch := e.downloadBeginCh
		e.downloadMu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ch:
			// A download began; loop to consume it.
		}
	}
}

// waitNextDownload implements ActionWaitDownload: it consumes the next began
// download and blocks until it reaches a terminal state (completed/cancelled),
// then returns its final info with the on-disk path and size. It honours the
// action context's deadline.
func (e *ChromeEngine) waitNextDownload(ctx context.Context) (*protocol.DownloadInfo, error) {
	guid, err := e.popNextDownloadGUID(ctx)
	if err != nil {
		return nil, err
	}
	for {
		e.downloadMu.Lock()
		info := e.downloads[guid]
		if info == nil {
			e.downloadMu.Unlock()
			return nil, fmt.Errorf("download %q disappeared from tracking", guid)
		}
		if info.State == protocol.DownloadCompleted || info.State == protocol.DownloadCancelled {
			cp := *info
			e.downloadMu.Unlock()
			// Authoritative size: re-stat the completed file on disk.
			if cp.State == protocol.DownloadCompleted && cp.Path != "" {
				if fi, statErr := os.Stat(cp.Path); statErr == nil {
					cp.ReceivedBytes = fi.Size()
					cp.TotalBytes = fi.Size()
				}
			}
			return &cp, nil
		}
		e.downloadMu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
