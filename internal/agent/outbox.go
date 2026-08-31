package agent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const outboxRecordVersion = "1"

type Limits struct {
	MaxItems int
	MaxBytes int64
}

type OutboxItem struct {
	ID      string
	Body    []byte
	Pending []string
	Path    string
}

type outboxRecord struct {
	Version string   `json:"version"`
	ID      string   `json:"id"`
	Body    []byte   `json:"body"`
	Pending []string `json:"pending"`
}

type Outbox struct {
	mu     sync.Mutex
	dir    string
	badDir string
	limits Limits
}

func NewOutbox(dir string, limits Limits) (*Outbox, error) {
	if limits.MaxItems <= 0 || limits.MaxBytes <= 0 {
		return nil, errors.New("outbox limits must be positive")
	}
	queueDir := filepath.Join(dir, "outbox")
	badDir := filepath.Join(dir, "outbox-corrupt")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(queueDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		return nil, err
	}
	return &Outbox{dir: queueDir, badDir: badDir, limits: limits}, nil
}

func (q *Outbox) Put(body []byte, destinations []string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(body) == 0 || len(destinations) == 0 {
		return "", errors.New("outbox report and destinations are required")
	}
	pending := uniqueDestinations(destinations)
	if len(pending) == 0 {
		return "", errors.New("outbox destinations are invalid")
	}
	id, err := newOutboxID()
	if err != nil {
		return "", err
	}
	record := outboxRecord{Version: outboxRecordVersion, ID: id, Body: bytes.Clone(body), Pending: pending}
	path := filepath.Join(q.dir, id+".json")
	if err := writeAtomic(path, mustMarshalRecord(record)); err != nil {
		return "", err
	}
	if err := q.enforceLimitsLocked(); err != nil {
		return "", err
	}
	return id, nil
}

func (q *Outbox) List() ([]OutboxItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.listLocked()
}

func (q *Outbox) Ack(id, destination string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id == "" || destination == "" {
		return errors.New("outbox id and destination are required")
	}
	path := filepath.Join(q.dir, filepath.Base(id)+".json")
	record, err := readOutboxRecord(path)
	if err != nil {
		return err
	}
	pending := record.Pending[:0]
	for _, current := range record.Pending {
		if current != destination {
			pending = append(pending, current)
		}
	}
	record.Pending = pending
	if len(record.Pending) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDirectory(q.dir)
	}
	if err := writeAtomic(path, mustMarshalRecord(record)); err != nil {
		return err
	}
	return syncDirectory(q.dir)
}

func (q *Outbox) Depth() int {
	items, err := q.List()
	if err != nil {
		return 0
	}
	return len(items)
}

func (q *Outbox) listLocked() ([]OutboxItem, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := make([]OutboxItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(q.dir, entry.Name())
		record, err := readOutboxRecord(path)
		if err != nil {
			if quarantineErr := q.quarantineLocked(path); quarantineErr != nil {
				return nil, fmt.Errorf("quarantine corrupt outbox record: %w", quarantineErr)
			}
			continue
		}
		items = append(items, OutboxItem{ID: record.ID, Body: bytes.Clone(record.Body), Pending: append([]string(nil), record.Pending...), Path: path})
	}
	return items, nil
}

func (q *Outbox) enforceLimitsLocked() error {
	items, err := q.listLocked()
	if err != nil {
		return err
	}
	var total int64
	for _, item := range items {
		total += itemCost(item)
	}
	for len(items) > q.limits.MaxItems || total > q.limits.MaxBytes {
		oldest := items[0]
		if err := os.Remove(oldest.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= itemCost(oldest)
		items = items[1:]
	}
	return syncDirectory(q.dir)
}

func itemCost(item OutboxItem) int64 {
	total := int64(len(item.Body))
	for _, destination := range item.Pending {
		total += int64(len(destination))
	}
	return total
}

func readOutboxRecord(path string) (outboxRecord, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return outboxRecord{}, err
	}
	var record outboxRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return outboxRecord{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return outboxRecord{}, errors.New("outbox record has trailing JSON")
	}
	if record.Version != outboxRecordVersion || record.ID == "" || len(record.Body) == 0 || len(record.Pending) == 0 {
		return outboxRecord{}, errors.New("invalid outbox record")
	}
	return record, nil
}

func mustMarshalRecord(record outboxRecord) []byte {
	body, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	return body
}

func uniqueDestinations(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func newOutboxID() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("%020d-%s", time.Now().UnixNano(), hex.EncodeToString(random)), nil
}

func (q *Outbox) quarantineLocked(path string) error {
	target := filepath.Join(q.badDir, filepath.Base(path))
	if err := os.Rename(path, target); err != nil {
		return err
	}
	entries, err := os.ReadDir(q.badDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for len(entries) > 16 {
		if err := os.Remove(filepath.Join(q.badDir, entries[0].Name())); err != nil {
			return err
		}
		entries = entries[1:]
	}
	return syncDirectory(q.badDir)
}
