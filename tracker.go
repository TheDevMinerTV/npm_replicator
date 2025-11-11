package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
)

type TrackerData struct {
	LastSeenSeq int64 `json:"last_seen_seq"`
}

type Tracker struct {
	fd *os.File
	m  sync.Mutex

	data *TrackerData
}

func NewTracker(file string, defaultData TrackerData) (*Tracker, error) {
	fd, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	fileInfo, err := fd.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting fd info: %w", err)
	}

	if fileInfo.Size() == 0 {
		if _, err := fd.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("error seeking to 0: %w", err)
		}

		enc := json.NewEncoder(fd)
		enc.SetIndent("", "  ")
		if err := enc.Encode(defaultData); err != nil {
			return nil, fmt.Errorf("error encoding default data: %w", err)
		}

		_, err = fd.Seek(0, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("error seeking fd: %w", err)
		}
	} else {
		_, err = fd.Seek(0, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("error seeking fd: %w", err)
		}
	}

	var data TrackerData
	if err := json.NewDecoder(fd).Decode(&data); err != nil {
		return nil, err
	}

	return &Tracker{fd: fd, data: &data}, nil
}

func (t *Tracker) Save() error {
	if _, err := t.fd.Seek(0, io.SeekStart); err != nil {
		return err
	}

	enc := json.NewEncoder(t.fd)
	enc.SetIndent("", "  ")
	if err := enc.Encode(t.data); err != nil {
		return err
	}

	return nil
}

func (t *Tracker) Close() {
	if err := t.Save(); err != nil {
		log.Error().Err(err).Msg("failed to save tracker data")
	}

	if err := t.fd.Close(); err != nil {
		log.Error().Err(err).Msg("failed to close tracker data")
	}
}

func (t *Tracker) LastSeq() int64 {
	t.m.Lock()
	defer t.m.Unlock()

	return t.data.LastSeenSeq
}

func (t *Tracker) NewLastSeenSeq(lastSeq int64) {
	t.m.Lock()
	t.data.LastSeenSeq = lastSeq
	t.m.Unlock()

	t.Save()
}
