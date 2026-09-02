// Package collab implements revisioned text operations and durable reconnect
// history. Clients apply edits immediately, send one splice, and transform it
// through operations that arrived while they were offline.
package collab

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

const historyLimit = 4096

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
	content  string
	revision int64
	history  []Operation
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
	oldest := doc.revision - int64(len(doc.history))
	if op.Base < oldest {
		return op, fmt.Errorf("edit is too old to reconcile; reload revision %d", doc.revision)
	}
	for _, other := range doc.history {
		if other.Revision <= op.Base {
			continue
		}
		op.From, op.To = transform(op.From, op.To, other)
	}
	runes := []rune(doc.content)
	if op.From < 0 || op.To < op.From || op.To > len(runes) {
		return op, errors.New("edit range is outside the document")
	}
	next := string(runes[:op.From]) + op.Insert + string(runes[op.To:])
	doc.revision++
	op.Revision = doc.revision
	doc.content = next
	doc.history = append(doc.history, op)
	if len(doc.history) > historyLimit {
		doc.history = doc.history[len(doc.history)-historyLimit:]
	}
	history, _ := json.Marshal(doc.history)
	if err := m.store.SaveCollabDocument(store.CollabDocument{NetworkID: op.NetworkID,
		ProjectID: op.ProjectID, Path: op.Path, Content: next, Revision: doc.revision,
		History: string(history)}); err != nil {
		return op, err
	}
	if err := write(next); err != nil {
		return op, err
	}
	return op, nil
}

func (m *Manager) load(networkID, projectID, path, diskContent string) (*document, error) {
	k := key(networkID, projectID, path)
	if doc := m.docs[k]; doc != nil {
		return doc, nil
	}
	row, err := m.store.CollabDocument(networkID, projectID, path)
	doc := &document{content: diskContent}
	if err == nil {
		doc.content, doc.revision = row.Content, row.Revision
		_ = json.Unmarshal([]byte(row.History), &doc.history)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	m.docs[k] = doc
	return doc, nil
}

func transform(from, to int, other Operation) (int, int) {
	removed := other.To - other.From
	inserted := len([]rune(other.Insert))
	delta := inserted - removed
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
