package main

import (
	"encoding/json"
	"os"
	"sync"

	"ncore_automation/api"
)

const historyFile = "history.json"

// RunRecord represents a single execution of the checker
type RunRecord = api.RunRecord

// HistoryManager handles reading and writing the history file
type HistoryManager struct {
	mu      sync.Mutex
	Records []RunRecord
}

// NewHistoryManager creates a new manager and loads existing history
func NewHistoryManager() (*HistoryManager, error) {
	hm := &HistoryManager{
		Records: []RunRecord{},
	}
	if err := hm.Load(); err != nil {
		return nil, err
	}
	return hm, nil
}

// Load reads the history from the JSON file
func (hm *HistoryManager) Load() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	data, err := os.ReadFile(historyFile)
	if os.IsNotExist(err) {
		return nil // It's okay if file doesn't exist yet
	}
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	return json.Unmarshal(data, &hm.Records)
}

// SaveRecord adds a new record and saves to file
func (hm *HistoryManager) SaveRecord(record RunRecord) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Prepend for newest first, or append? Let's use append and sort or just append.
	// Usually newest first is better for display, but appending is faster.
	// Let's just append and reverse on display if needed.
	hm.Records = append(hm.Records, record)

	return hm.saveToFile()
}

func (hm *HistoryManager) saveToFile() error {
	data, err := json.MarshalIndent(hm.Records, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(historyFile, data, 0644)
}

// GetRecords returns a copy of records (could be sorted)
func (hm *HistoryManager) GetRecords() []RunRecord {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Return copy to avoid race conditions
	result := make([]RunRecord, len(hm.Records))
	copy(result, hm.Records)

	// Reverse order (newest first)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}
