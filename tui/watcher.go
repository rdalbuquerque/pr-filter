package tui

import (
	"log"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
)

// WatchDataFiles watches both the data file and the AI evaluations file for
// changes and sends dataFileChangedMsg to the program whenever either file
// is updated. It blocks forever. evalPath may be empty to skip AI evaluation watching.
func WatchDataFiles(dataPath, evalPath string, p *tea.Program) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher error: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the data directory
	dir := filepath.Dir(dataPath)
	if err := watcher.Add(dir); err != nil {
		log.Printf("watch dir error: %v", err)
		return
	}

	// If eval file is in a different directory, watch that too
	if evalPath != "" {
		evalDir := filepath.Dir(evalPath)
		if evalDir != dir {
			if err := watcher.Add(evalDir); err != nil {
				log.Printf("watch eval dir error: %v", err)
				// Non-fatal: we can still watch the data file
			}
		}
	}

	dataBase := filepath.Base(dataPath)
	evalBase := ""
	if evalPath != "" {
		evalBase = filepath.Base(evalPath)
	}

	// Debounce: coalesce rapid writes
	var debounce *time.Timer
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			name := filepath.Base(event.Name)
			isDataFile := name == dataBase
			isEvalFile := evalBase != "" && name == evalBase
			if (isDataFile || isEvalFile) &&
				(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, func() {
					sendMergedUpdate(dataPath, evalPath, p)
				})
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func sendMergedUpdate(dataPath, evalPath string, p *tea.Program) {
	df, err := prdata.LoadDataFile(dataPath)
	if err != nil {
		return
	}

	msg := dataFileChangedMsg{PRs: df.PRs, Stats: df.Stats}

	// Load AI evaluations if available
	if evalPath != "" {
		ef, err := prdata.LoadAIEvaluationsFile(evalPath)
		if err == nil && ef != nil {
			msg.Evaluations = ef.Evaluations
		}
	}

	p.Send(msg)
}

// WatchDataFile is the legacy single-file watcher for backward compatibility.
func WatchDataFile(dataPath string, p *tea.Program) {
	WatchDataFiles(dataPath, "", p)
}
