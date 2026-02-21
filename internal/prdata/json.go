package prdata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadDataFile reads and parses the intermediate JSON data file.
func LoadDataFile(path string) (*DataFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read data file: %w", err)
	}

	var df DataFile
	if err := json.Unmarshal(data, &df); err != nil {
		return nil, fmt.Errorf("parse data file: %w", err)
	}

	return &df, nil
}

// MarshalDataFile returns the JSON representation of the data file.
func MarshalDataFile(df *DataFile) ([]byte, error) {
	return json.MarshalIndent(df, "", "  ")
}

// SaveDataFile atomically writes the data file (write to .tmp, then rename).
func SaveDataFile(path string, df *DataFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	data, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal data file: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
