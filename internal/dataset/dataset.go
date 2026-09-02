// Package dataset stores immutable dataset versions as content-addressed
// chunks. Identical content is written once and manifests are cheap to move
// between workers.
package dataset

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

const ChunkSize = 4 << 20

type Chunk struct {
	Hash string `json:"hash"`
	Size int    `json:"size"`
}
type File struct {
	Path   string  `json:"path"`
	Size   int64   `json:"size"`
	Mode   uint32  `json:"mode"`
	Chunks []Chunk `json:"chunks"`
}
type Manifest struct {
	ID        string `json:"id"`
	NetworkID string `json:"network_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	RootHash  string `json:"root_hash"`
	Files     []File `json:"files"`
	SizeBytes int64  `json:"size_bytes"`
}

type Manager struct {
	layout config.Layout
	store  *store.Store
}

func New(layout config.Layout, st *store.Store) *Manager { return &Manager{layout: layout, store: st} }

func ValidName(name string) bool {
	if name == "" || len(name) > 80 || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (m *Manager) Register(networkID, nodeID, name, version, source string) (store.Dataset, error) {
	if !ValidName(name) {
		return store.Dataset{}, errors.New("dataset names use letters, numbers, dots, dashes and underscores")
	}
	if version == "" {
		version = "v1"
	}
	if !ValidName(version) {
		return store.Dataset{}, errors.New("invalid dataset version")
	}
	stat, err := os.Stat(source)
	if err != nil {
		return store.Dataset{}, err
	}
	if !stat.IsDir() {
		return store.Dataset{}, errors.New("dataset source must be a directory")
	}
	manifest := Manifest{ID: config.NewID(), NetworkID: networkID, Name: name, Version: version, Files: []File{}}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("dataset contains unsupported file %s", entry.Name())
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		item := File{Path: filepath.ToSlash(rel), Size: info.Size(), Mode: uint32(info.Mode().Perm()), Chunks: []Chunk{}}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		reader := bufio.NewReaderSize(file, ChunkSize)
		for {
			buf := make([]byte, ChunkSize)
			n, readErr := io.ReadFull(reader, buf)
			if n > 0 {
				buf = buf[:n]
				sum := sha256.Sum256(buf)
				hash := hex.EncodeToString(sum[:])
				if err := m.putChunk(hash, buf); err != nil {
					file.Close()
					return err
				}
				item.Chunks = append(item.Chunks, Chunk{Hash: hash, Size: n})
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				break
			}
			if readErr != nil {
				file.Close()
				return readErr
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, item)
		manifest.SizeBytes += info.Size()
		return nil
	})
	if err != nil {
		return store.Dataset{}, err
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	rootInput, _ := json.Marshal(manifest.Files)
	root := sha256.Sum256(rootInput)
	manifest.RootHash = hex.EncodeToString(root[:])
	raw, _ := json.Marshal(manifest)
	dataset := store.Dataset{ID: manifest.ID, NetworkID: networkID, Name: name, Version: version, RootHash: manifest.RootHash, FileCount: len(manifest.Files), SizeBytes: manifest.SizeBytes, Manifest: string(raw)}
	if err := m.store.CreateDataset(&dataset); err != nil {
		return store.Dataset{}, err
	}
	_ = m.store.SetDatasetPlacement(store.DatasetPlacement{DatasetID: dataset.ID, NodeID: nodeID, State: "ready", BytesDone: dataset.SizeBytes})
	return dataset, nil
}

func (m *Manager) putChunk(hash string, raw []byte) error {
	dir := filepath.Join(m.layout.Datasets(), "chunks", hash[:2])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	target := filepath.Join(dir, hash)
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (m *Manager) Manifest(dataset store.Dataset) (Manifest, error) {
	var manifest Manifest
	err := json.Unmarshal([]byte(dataset.Manifest), &manifest)
	return manifest, err
}

func (m *Manager) Export(dataset store.Dataset) (map[string][]byte, error) {
	manifest, err := m.Manifest(dataset)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, file := range manifest.Files {
		for _, chunk := range file.Chunks {
			if _, ok := out[chunk.Hash]; ok {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(m.layout.Datasets(), "chunks", chunk.Hash[:2], chunk.Hash))
			if err != nil {
				return nil, err
			}
			out[chunk.Hash] = raw
		}
	}
	return out, nil
}

func (m *Manager) Import(dataset store.Dataset, chunks map[string][]byte, nodeID string) error {
	manifest, err := m.Manifest(dataset)
	if err != nil {
		return err
	}
	for hash, raw := range chunks {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != hash {
			return errors.New("dataset chunk checksum mismatch")
		}
		if err := m.putChunk(hash, raw); err != nil {
			return err
		}
	}
	for _, file := range manifest.Files {
		for _, chunk := range file.Chunks {
			if _, err := os.Stat(filepath.Join(m.layout.Datasets(), "chunks", chunk.Hash[:2], chunk.Hash)); err != nil {
				return fmt.Errorf("dataset is missing chunk %s", chunk.Hash)
			}
		}
	}
	if err := m.store.UpsertDataset(dataset); err != nil {
		return err
	}
	return m.store.SetDatasetPlacement(store.DatasetPlacement{DatasetID: dataset.ID, NodeID: nodeID, State: "ready", BytesDone: dataset.SizeBytes})
}

func (m *Manager) Materialize(dataset store.Dataset, target string) error {
	manifest, err := m.Manifest(dataset)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}
	for _, item := range manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(item.Path))
		if clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("unsafe dataset manifest path")
		}
		path := filepath.Join(target, clean)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(item.Mode))
		if err != nil {
			return err
		}
		for _, chunk := range item.Chunks {
			src, err := os.Open(filepath.Join(m.layout.Datasets(), "chunks", chunk.Hash[:2], chunk.Hash))
			if err != nil {
				file.Close()
				return err
			}
			_, copyErr := io.Copy(file, src)
			src.Close()
			if copyErr != nil {
				file.Close()
				return copyErr
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}
