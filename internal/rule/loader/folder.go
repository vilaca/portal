package loader

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"sigs.k8s.io/yaml"

	"github.com/vilaca/portal/internal/api"
)

// folderDebounce is the quiet period applied to rapid filesystem events
// before re-walking the directory and emitting a new snapshot.
const folderDebounce = 100 * time.Millisecond

// Folder is a RuleLoader that walks a directory, parses every *.yaml / *.yml
// file into one or more api.Rule, and watches the directory via fsnotify so
// changes trigger a re-emit. Parse errors per file are logged and skipped —
// they do not abort the loader.
type Folder struct {
	path string

	mu      sync.Mutex
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewFolder returns a filesystem-backed RuleLoader rooted at path.
func NewFolder(path string) api.RuleLoader {
	return &Folder{path: path}
}

// Name returns "folder".
func (f *Folder) Name() string { return "folder" }

// Start walks the directory once synchronously (so the initial onUpdate fires
// before Start returns), then begins watching for changes in a background
// goroutine. The watcher and goroutine live until Stop or ctx cancellation.
func (f *Folder) Start(ctx context.Context, onUpdate func(snapshot []api.Rule)) error {
	// Initial scan + emit.
	snap := f.scan()
	onUpdate(snap)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("folder loader: %w", err)
	}
	if err := w.Add(f.path); err != nil {
		_ = w.Close()
		return fmt.Errorf("folder loader: watch %q: %w", f.path, err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	f.watcher = w
	f.cancel = cancel
	f.done = make(chan struct{})
	done := f.done
	f.mu.Unlock()

	go f.run(childCtx, w, onUpdate, done)
	return nil
}

// Stop tears down the watcher and waits for the background goroutine to exit.
func (f *Folder) Stop(ctx context.Context) error {
	f.mu.Lock()
	w := f.watcher
	cancel := f.cancel
	done := f.done
	f.watcher = nil
	f.cancel = nil
	f.done = nil
	f.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if w != nil {
		_ = w.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *Folder) run(ctx context.Context, w *fsnotify.Watcher, onUpdate func([]api.Rule), done chan struct{}) {
	defer close(done)

	var (
		timer    *time.Timer
		timerC   <-chan time.Time
		resetTimer = func() {
			if timer == nil {
				timer = time.NewTimer(folderDebounce)
			} else {
				if !timer.Stop() {
					// Drain if already fired but not received.
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(folderDebounce)
			}
			timerC = timer.C
		}
	)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// Only YAML files trigger a re-scan; ignore irrelevant churn.
			if isYAML(ev.Name) {
				resetTimer()
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("folder loader: watcher error", "err", err, "path", f.path)
		case <-timerC:
			timerC = nil
			snap := f.scan()
			onUpdate(snap)
		}
	}
}

// scan walks the directory and returns every parsed rule. Errors per file are
// logged and the file is skipped.
func (f *Folder) scan() []api.Rule {
	var out []api.Rule
	entries, err := os.ReadDir(f.path)
	if err != nil {
		slog.Warn("folder loader: read dir failed", "err", err, "path", f.path)
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isYAML(name) {
			continue
		}
		full := filepath.Join(f.path, name)
		rules, perr := parseFile(full)
		if perr != nil {
			slog.Warn("folder loader: parse error", "err", perr, "path", full)
			continue
		}
		out = append(out, rules...)
	}
	return out
}

// parseFile reads path and returns either one rule (top-level mapping) or
// many (top-level YAML list). Source.Origin is set to "folder" and
// Source.Path to the file path.
func parseFile(path string) ([]api.Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Empty file is a no-op rather than an error.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	// Try list first; if that fails with a type error, fall back to single.
	var asList []api.Rule
	if err := yaml.Unmarshal(data, &asList); err == nil && looksLikeList(data) {
		for i := range asList {
			asList[i].Source = api.RuleSource{Origin: "folder", Path: path}
		}
		return asList, nil
	}
	var single api.Rule
	if err := yaml.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	single.Source = api.RuleSource{Origin: "folder", Path: path}
	return []api.Rule{single}, nil
}

// looksLikeList returns true if the first non-whitespace, non-comment
// character of data is '-' or '['. sigs.k8s.io/yaml accepts both an object
// and a list into a struct slice via JSON coercion in some cases, so we
// disambiguate cheaply by syntax.
func looksLikeList(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		return trimmed[0] == '-' || trimmed[0] == '['
	}
	return false
}

func isYAML(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
