package deepseekweb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DirName is the subdirectory of DATA_DIR holding the sessions and their key.
const DirName = "deepseek-web"

var (
	defMu  sync.Mutex
	defMgr *Manager
	defDir func() string
)

// SetDataDirResolver tells the package where DATA_DIR is (wired at startup).
func SetDataDirResolver(f func() string) {
	defMu.Lock()
	defer defMu.Unlock()
	defDir = f
}

// SetDefault replaces the process-wide manager (tests, embedders).
func SetDefault(m *Manager) {
	defMu.Lock()
	defer defMu.Unlock()
	defMgr = m
}

// Default opens the process-wide manager on first use.
func Default() (*Manager, error) {
	defMu.Lock()
	defer defMu.Unlock()
	if defMgr != nil {
		return defMgr, nil
	}
	if defDir == nil {
		return nil, errors.New("DATA_DIR no configurado para sesiones DeepSeek Web")
	}
	m, err := Open(Options{Dir: filepath.Join(defDir(), DirName), KeyFile: strings.TrimSpace(os.Getenv(EnvKeyFile))})
	if err != nil {
		return nil, err
	}
	defMgr = m
	return m, nil
}

// DefaultFallbackAllowed is the gate wired into the DeepSeek profile. With no
// sessions stored it answers false without creating any file, so installs that
// never used the panel keep the paid fallback off.
func DefaultFallbackAllowed() bool {
	defMu.Lock()
	m, f := defMgr, defDir
	defMu.Unlock()
	if m == nil {
		if f == nil {
			return false
		}
		if _, err := os.Stat(filepath.Join(f(), DirName, stateFile)); err != nil {
			return false
		}
		var err error
		if m, err = Default(); err != nil {
			return false
		}
	}
	return m.FallbackAllowed()
}
