package main

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

type WAL struct {
	file     *os.File
	path     string
	writeLsn uint64
}

// NewWAL opens or creates a write-ahead log at the given path.
func NewWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	w := &WAL{
		file:     f,
		path:     path,
		writeLsn: 1, // LSN starts at 1
	}
	// Set LSN to next available
	entries, err := w.ReadFrom(1)
	if err != nil && !errors.Is(err, io.EOF) {
		f.Close()
		return nil, err
	}
	w.writeLsn = uint64(len(entries)) + 1
	return w, nil
}

// Append a record, returns its LSN.
func (w *WAL) Append(entry []byte) (uint64, error) {
	lsn := w.writeLsn
	length := uint32(len(entry))
	crc := crc32.ChecksumIEEE(entry)
	head := make([]byte, 8+4)
	binary.BigEndian.PutUint64(head[0:8], lsn)
	binary.BigEndian.PutUint32(head[8:12], length)
	trailer := make([]byte, 4)
	binary.BigEndian.PutUint32(trailer, crc)
	if _, err := w.file.Write(head); err != nil {
		return 0, err
	}
	if _, err := w.file.Write(entry); err != nil {
		return 0, err
	}
	if _, err := w.file.Write(trailer); err != nil {
		return 0, err
	}
	w.writeLsn++
	return lsn, nil
}

// ReadFrom returns all valid entries from the given LSN. Stops at first corrupted record.
func (w *WAL) ReadFrom(lsn uint64) ([][]byte, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	entries := [][]byte{}
	for {
		head := make([]byte, 12)
		_, err := io.ReadFull(w.file, head)
		if err == io.EOF {
			break
		}
		if err != nil {
			// Partial/corrupt
			return entries, nil
		}
		recLsn := binary.BigEndian.Uint64(head[:8])
		recLen := binary.BigEndian.Uint32(head[8:12])
		if recLsn < lsn {
			// Skip
			if _, err := w.file.Seek(int64(recLen+4), io.SeekCurrent); err != nil {
				return entries, nil
			}
			continue
		}
		payload := make([]byte, recLen)
		_, err = io.ReadFull(w.file, payload)
		if err != nil {
			return entries, nil
		}
		trailer := make([]byte, 4)
		_, err = io.ReadFull(w.file, trailer)
		if err != nil {
			return entries, nil
		}
		crc := binary.BigEndian.Uint32(trailer)
		if crc32.ChecksumIEEE(payload) != crc {
			// Corrupt, stop
			return entries, nil
		}
		entries = append(entries, payload)
	}
	return entries, nil
}

func (w *WAL) Sync() error {
	return w.file.Sync()
}

func (w *WAL) Close() error {
	return w.file.Close()
}
