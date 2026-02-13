package tui

import (
	"log"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/revelo/pr-filter/internal/prdata"
)

// WatchDataFile watches the data file for changes and sends dataFileChangedMsg
// to the program whenever the file is updated. It blocks forever.
func WatchDataFile(dataPath string, p *tea.Program) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher error: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the directory (atomic rename triggers CREATE in the dir)
	dir := filepath.Dir(dataPath)
	if err := watcher.Add(dir); err != nil {
		log.Printf("watch dir error: %v", err)
		return
	}

	base := filepath.Base(dataPath)

	// Debounce: the fetcher may write rapidly, coalesce events
	var debounce *time.Timer
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) == base &&
				(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, func() {
					df, err := prdata.LoadDataFile(dataPath)
					if err != nil {
						return
					}
					p.Send(dataFileChangedMsg{PRs: df.PRs, Stats: df.Stats})
				})
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
