package main

import (
	"bytes"
	"math/rand"
	"os"
	"testing"
)

func TestWALAppendReadAndCorruption(t *testing.T) {
	const (
		walPath    = "testwal.log"
		entryCount = 10000
		entrySize  = 64
	)
	defer os.Remove(walPath)
	w, err := NewWAL(walPath)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	// Write entries
	entries := make([][]byte, entryCount)
	for i := range entries {
		val := make([]byte, entrySize)
		for j := range val {
			val[j] = byte(rand.Intn(256))
		}
		entries[i] = val
		lsn, err := w.Append(val)
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
		if lsn != uint64(i+1) {
			t.Errorf("Expected LSN %d, got %d", i+1, lsn)
		}
	}
	w.Sync()
	w.Close()

	// Reopen and confirm all entries readable
	w, err = NewWAL(walPath)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	readEntries, err := w.ReadFrom(1)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if len(readEntries) != entryCount {
		t.Errorf("Expected %d entries, got %d", entryCount, len(readEntries))
	}
	for i, e := range readEntries {
		if !bytes.Equal(e, entries[i]) {
			t.Errorf("Entry %d mismatch", i)
		}
	}
	w.Close()

	// Simulate crash by truncating file mid-record
	f, err := os.OpenFile(walPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Open for truncate: %v", err)
	}
	fileInfo, _ := f.Stat()
	truncateAt := fileInfo.Size() - int64(entrySize/2) // Truncate halfway into the last record
	if truncateAt < 0 {
		truncateAt = fileInfo.Size()
	}
	if err := f.Truncate(truncateAt); err != nil {
		t.Fatalf("truncate err: %v", err)
	}
	f.Close()

	w, err = NewWAL(walPath)
	if err != nil {
		t.Fatalf("Reopen after truncate: %v", err)
	}
	readEntries, err = w.ReadFrom(1)
	if err != nil {
		t.Fatalf("ReadFrom after truncate: %v", err)
	}
	w.Close()
	// Should return strictly less than entryCount
	if len(readEntries) >= entryCount {
		t.Errorf("Expected less than %d entries after corruption, got %d", entryCount, len(readEntries))
	}
}
