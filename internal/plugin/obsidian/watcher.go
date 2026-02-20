package obsidian

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	plugin  *Plugin
	watcher *fsnotify.Watcher
	mu      sync.Mutex
	pending map[string]time.Time
	done    chan struct{}
}

func NewWatcher(p *Plugin) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Recursively add all directories.
	err = filepath.WalkDir(p.cfg.VaultPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return fw.Add(path)
		}
		return nil
	})
	if err != nil {
		fw.Close()
		return nil, err
	}

	return &Watcher{
		plugin:  p,
		watcher: fw,
		pending: make(map[string]time.Time),
		done:    make(chan struct{}),
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	go w.loop(ctx)
	return nil
}

func (w *Watcher) Stop() error {
	close(w.done)
	return w.watcher.Close()
}

func (w *Watcher) loop(ctx context.Context) {
	debounce := time.NewTicker(250 * time.Millisecond)
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.plugin.log.Warn("fsnotify error", "error", err)
		case <-debounce.C:
			w.flush()
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	name := filepath.Base(event.Name)

	// Ignore dotfiles.
	if strings.HasPrefix(name, ".") {
		return
	}

	// New directory: add to watcher.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.watcher.Add(event.Name)
			return
		}
	}

	// Only care about .md files.
	if !strings.HasSuffix(name, ".md") {
		return
	}

	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) {
		w.mu.Lock()
		w.pending[event.Name] = time.Now()
		w.mu.Unlock()
	}
}

func (w *Watcher) flush() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}

	cutoff := time.Now().Add(-500 * time.Millisecond)
	ready := make(map[string]struct{})
	for path, t := range w.pending {
		if t.Before(cutoff) {
			ready[path] = struct{}{}
			delete(w.pending, path)
		}
	}
	w.mu.Unlock()

	for absPath := range ready {
		relPath, err := filepath.Rel(w.plugin.cfg.VaultPath, absPath)
		if err != nil {
			continue
		}
		if err := w.plugin.IndexFile(relPath); err != nil {
			w.plugin.log.Warn("watcher re-index failed", "path", relPath, "error", err)
		} else {
			w.plugin.log.Debug("watcher re-indexed", "path", relPath)
		}
	}
}
