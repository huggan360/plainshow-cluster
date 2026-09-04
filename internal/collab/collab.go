// Package collab implements revisioned text operations and durable reconnect
// history. Clients apply edits immediately, send one splice, and transform it
// through operations that arrived while they were offline.
package collab

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

const historyLimit = 4096

// trimEvery is how many edits pass between retention sweeps. Rare enough to
// stay off the keystroke path, often enough that the log cannot run away.
const trimEvery = 512

// ErrDuplicate means the same browser operation already reached this clone,
// normally through another tab connected to the controller relay.
var ErrDuplicate = errors.New("edit was already applied")

type Operation struct {
	NetworkID string `json:"network_id"`
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	Path      string `json:"path"`
	ClientID  string `json:"client_id"`
	Sequence  int64  `json:"sequence"`
	Base      int64  `json:"base_revision"`
	Revision  int64  `json:"revision"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	Insert    string `json:"insert"`
}

type Snapshot struct {
	Content  string `json:"content"`
	Revision int64  `json:"revision"`
}

type WriteFunc func(content string) error

type document struct {
	content   string
	revision  int64
	history   []Operation
	seen      map[string]int64
	sinceTrim int
}

type Manager struct {
	store *store.Store
	mu    sync.Mutex
	docs  map[string]*document
}

func New(st *store.Store) *Manager { return &Manager{store: st, docs: make(map[string]*document)} }

func key(networkID, projectID, path string) string {
	return networkID + "\x00" + projectID + "\x00" + path
}

func (m *Manager) Open(networkID, projectID, path, diskContent string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load(networkID, projectID, path, diskContent)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Content: doc.content, Revision: doc.revision}, nil
}

// Reset forgets collaboration history after a file is replaced outside the
// editor, so the next open uses the new disk content instead of a stale cache.
func (m *Manager) Reset(networkID, projectID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.DeleteCollabDocument(networkID, projectID, path); err != nil {
		return err
	}
	delete(m.docs, key(networkID, projectID, path))
	return nil
}

// ResetTree is Reset for a directory rename or removal.
func (m *Manager) ResetTree(networkID, projectID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.DeleteCollabTree(networkID, projectID, path); err != nil {
		return err
	}
	prefix := key(networkID, projectID, "")
	for k := range m.docs {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rel := strings.TrimPrefix(k, prefix)
		if rel == path || strings.HasPrefix(rel, path+"/") {
			delete(m.docs, k)
		}
	}
	return nil
}

func (m *Manager) Apply(op Operation, diskContent string, write WriteFunc) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load(op.NetworkID, op.ProjectID, op.Path, diskContent)
	if err != nil {
		return op, err
	}
	if op.Base > doc.revision {
		return op, errors.New("edit revision is ahead of the server")
	}
	if op.ClientID != "" && op.Sequence > 0 && op.Sequence <= doc.seen[op.ClientID] {
		return op, ErrDuplicate
	}
	oldest := doc.revision - int64(len(doc.history))
	if op.Base < oldest {
		return op, fmt.Errorf("edit is too old to reconcile; reload revision %d", doc.revision)
	}
	for _, other := range doc.history {
		if other.Revision <= op.Base {
			continue
		}
		op.From, op.To = transform(op, other)
	}
	runes := []rune(doc.content)
	if op.From < 0 || op.To < op.From || op.To > len(runes) {
		return op, errors.New("edit range is outside the document")
	}
	next := string(runes[:op.From]) + op.Insert + string(runes[op.To:])
	op.Revision = doc.revision + 1

	// One small insert per keystroke, whatever the history looks like. This
	// used to rewrite the whole history as a JSON blob, which cost about 5 ms
	// per edit early on and over 30 ms after a thousand.
	payload, err := json.Marshal(op)
	if err != nil {
		return op, err
	}
	if err := m.store.SaveCollabEdit(store.CollabDocument{
		NetworkID: op.NetworkID, ProjectID: op.ProjectID, Path: op.Path,
		Content: next, Revision: op.Revision,
	}, op.Revision, string(payload)); err != nil {
		return op, err
	}
	if err := write(next); err != nil {
		// The database now contains the operation, so retain the new in-memory
		// state. A later open/restart can repair the file from that durable
		// content instead of accepting another operation against stale text.
		doc.content = next
		doc.revision = op.Revision
		doc.history = append(doc.history, op)
		if op.ClientID != "" && op.Sequence > doc.seen[op.ClientID] {
			doc.seen[op.ClientID] = op.Sequence
		}
		return op, err
	}
	doc.content = next
	doc.revision = op.Revision
	doc.history = append(doc.history, op)
	if op.ClientID != "" && op.Sequence > doc.seen[op.ClientID] {
		doc.seen[op.ClientID] = op.Sequence
	}
	if len(doc.history) > historyLimit {
		doc.history = doc.history[len(doc.history)-historyLimit:]
	}

	// Retention is enforced off the keystroke path: a delete on every edit puts
	// a scan in front of every character typed, which is the cost just removed.
	doc.sinceTrim++
	if doc.sinceTrim >= trimEvery {
		doc.sinceTrim = 0
		if keepFrom := doc.revision - int64(historyLimit); keepFrom > 0 {
			// Retention is maintenance, not part of whether this already-durable
			// edit succeeded. A later sweep may retry after a transient failure.
			_ = m.store.TrimCollabOperations(op.NetworkID, op.ProjectID, op.Path, keepFrom)
		}
	}
	return op, nil
}

func (m *Manager) load(networkID, projectID, path, diskContent string) (*document, error) {
	k := key(networkID, projectID, path)
	if doc := m.docs[k]; doc != nil {
		return doc, nil
	}
	row, err := m.store.CollabDocument(networkID, projectID, path)
	doc := &document{content: diskContent, seen: map[string]int64{}}
	if err == nil {
		doc.content, doc.revision = row.Content, row.Revision
		doc.history = m.replayHistory(networkID, projectID, path, row.History)
		for _, op := range doc.history {
			if op.ClientID != "" && op.Sequence > doc.seen[op.ClientID] {
				doc.seen[op.ClientID] = op.Sequence
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	m.docs[k] = doc
	return doc, nil
}

func transform(incoming, other Operation) (int, int) {
	from, to := incoming.From, incoming.To
	removed := other.To - other.From
	inserted := len([]rune(other.Insert))
	delta := inserted - removed
	// Two peers inserting at the same position must choose the same order no
	// matter which edit reached a particular clone first. Without this tie
	// break, clone A produced "AB" while clone B produced "BA".
	if removed == 0 && from == to && other.From == from {
		if operationOrder(other) < operationOrder(incoming) {
			return from + inserted, to + inserted
		}
		return from, to
	}
	if other.To <= from {
		return from + delta, to + delta
	}
	if other.From >= to {
		return from, to
	}
	// Overlapping concurrent replacements converge by keeping the incoming
	// operation anchored at the beginning of the changed region.
	start := from
	if other.From < start {
		start = other.From + inserted
	}
	end := to + delta
	if end < start {
		end = start
	}
	return start, end
}

func operationOrder(op Operation) string {
	return op.ClientID + "\x00" + fmt.Sprintf("%020d", op.Sequence)
}

// replayHistory rebuilds a document's recent operations.
//
// It reads the append-only log, and falls back to the JSON blob written by
// older versions so a node upgraded mid-edit does not lose the ability to
// reconcile a client that was already connected.
func (m *Manager) replayHistory(networkID, projectID, path, legacy string) []Operation {
	history := []Operation{}
	payloads, err := m.store.CollabOperations(networkID, projectID, path, historyLimit)
	if err == nil {
		for _, payload := range payloads {
			var op Operation
			if json.Unmarshal([]byte(payload), &op) == nil {
				history = append(history, op)
			}
		}
	}
	if len(history) == 0 && legacy != "" && legacy != "[]" {
		_ = json.Unmarshal([]byte(legacy), &history)
	}
	return history
}
