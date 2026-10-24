package app

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
)

func Export(st *config.Store, slug, ext, text string) (string, error) {
	if st == nil {
		return "", fmt.Errorf("no config directory to write to")
	}
	path := filepath.Join(st.ExportsDir(), output.ExportName(slug, ext, time.Now()))
	f, where, err := output.Open(path, "", "", time.Now())
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(f, text+"\n"); err != nil {
		f.Close()
		return "", err
	}
	return where, f.Close()
}
