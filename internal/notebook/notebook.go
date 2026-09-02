// Package notebook manages persistent Python kernels for browser notebooks.
//
// The wire protocol is deliberately tiny and uses only Python's standard
// library. A machine may use its existing Python/venv/Conda environment; the
// cluster does not silently install packages into it. State lives for the life
// of the kernel, so imports and variables survive from one cell to the next.
package notebook

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNoPython explains why a notebook cannot start on a bare machine.
var ErrNoPython = errors.New("python3 is not installed or is not on PATH")

// Output is one display, stdout, stderr, or error item from a cell.
type Output struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Result is one completed cell execution.
type Result struct {
	Execution int64         `json:"execution_count"`
	Outputs   []Output      `json:"outputs"`
	Duration  time.Duration `json:"duration_ns"`
}

type message struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type request struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// Manager owns at most one Python kernel per project.
type Manager struct {
	mu      sync.Mutex
	kernels map[string]*kernel
	next    atomic.Uint64
}

// NewManager creates an empty kernel manager.
func NewManager() *Manager { return &Manager{kernels: map[string]*kernel{}} }

// Available reports whether this process can launch Python.
func Available() bool {
	_, err := exec.LookPath("python3")
	return err == nil
}

type kernel struct {
	dir     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	started time.Time

	execMu  sync.Mutex
	mu      sync.Mutex
	pending map[string]chan message
	closed  bool
}

// Status describes a project's kernel without starting one.
type Status struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Started   string `json:"started_at"`
}

// Status returns the current kernel state for projectDir.
func (m *Manager) Status(projectDir string) Status {
	status := Status{Available: Available()}
	m.mu.Lock()
	k := m.kernels[projectDir]
	m.mu.Unlock()
	if k == nil {
		return status
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	status.Running = !k.closed && k.cmd.Process != nil
	if status.Running {
		status.PID = k.cmd.Process.Pid
		status.Started = k.started.UTC().Format(time.RFC3339)
	}
	return status
}

// Execute runs code in the project's persistent namespace.
func (m *Manager) Execute(ctx context.Context, projectDir, code string) (Result, error) {
	if !Available() {
		return Result{}, ErrNoPython
	}
	k, err := m.get(projectDir)
	if err != nil {
		return Result{}, err
	}
	k.execMu.Lock()
	defer k.execMu.Unlock()

	id := fmt.Sprintf("cell-%d", m.next.Add(1))
	ch := make(chan message, 128)
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return Result{}, errors.New("the Python kernel stopped")
	}
	k.pending[id] = ch
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		delete(k.pending, id)
		k.mu.Unlock()
	}()

	started := time.Now()
	encoded, _ := json.Marshal(request{ID: id, Code: code})
	if _, err := k.stdin.Write(append(encoded, '\n')); err != nil {
		return Result{}, fmt.Errorf("send cell to Python: %w", err)
	}

	result := Result{Execution: int64(m.next.Load()), Outputs: []Output{}}
	for {
		select {
		case <-ctx.Done():
			_ = k.interrupt()
			return result, ctx.Err()
		case event, ok := <-ch:
			if !ok {
				return result, errors.New("the Python kernel stopped during execution")
			}
			if event.Type == "done" {
				result.Duration = time.Since(started)
				return result, nil
			}
			result.Outputs = append(result.Outputs, Output{Type: event.Type, Text: event.Text})
		}
	}
}

func (m *Manager) get(projectDir string) (*kernel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.kernels[projectDir]; existing != nil {
		existing.mu.Lock()
		alive := !existing.closed
		existing.mu.Unlock()
		if alive {
			return existing, nil
		}
		delete(m.kernels, projectDir)
	}
	k, err := start(projectDir)
	if err != nil {
		return nil, err
	}
	m.kernels[projectDir] = k
	return k, nil
}

func start(dir string) (*kernel, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, ErrNoPython
	}
	cmd := exec.Command(python, "-u", "-c", kernelProgram)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	k := &kernel{dir: dir, cmd: cmd, stdin: stdin, pending: map[string]chan message{}}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	k.started = time.Now()
	go k.read(stdout)
	go io.Copy(io.Discard, stderr)
	go func() {
		_ = cmd.Wait()
		k.failAll()
	}()
	return k, nil
}

func (k *kernel) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		var event message
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.ID == "" {
			continue
		}
		k.mu.Lock()
		ch := k.pending[event.ID]
		k.mu.Unlock()
		if ch != nil {
			ch <- event
		}
	}
}

func (k *kernel) failAll() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	for id, ch := range k.pending {
		close(ch)
		delete(k.pending, id)
	}
}

func (k *kernel) interrupt() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed || k.cmd.Process == nil {
		return errors.New("the Python kernel is not running")
	}
	return k.cmd.Process.Signal(os.Interrupt)
}

// Interrupt asks the active Python execution to stop while keeping its state.
func (m *Manager) Interrupt(projectDir string) error {
	m.mu.Lock()
	k := m.kernels[projectDir]
	m.mu.Unlock()
	if k == nil {
		return errors.New("this project has no running kernel")
	}
	return k.interrupt()
}

// Restart removes a project's Python process. The next cell starts fresh.
func (m *Manager) Restart(projectDir string) error {
	m.mu.Lock()
	k := m.kernels[projectDir]
	delete(m.kernels, projectDir)
	m.mu.Unlock()
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed || k.cmd.Process == nil {
		return nil
	}
	return k.cmd.Process.Kill()
}

// Close stops every kernel owned by the daemon.
func (m *Manager) Close() {
	m.mu.Lock()
	kernels := make([]*kernel, 0, len(m.kernels))
	for _, k := range m.kernels {
		kernels = append(kernels, k)
	}
	m.kernels = map[string]*kernel{}
	m.mu.Unlock()
	for _, k := range kernels {
		k.mu.Lock()
		if !k.closed && k.cmd.Process != nil {
			_ = k.cmd.Process.Kill()
		}
		k.mu.Unlock()
	}
}

const kernelProgram = `
import ast, json, sys, traceback

namespace = {"__name__": "__main__"}
current_id = ""

def emit(kind, text=""):
    sys.__stdout__.write(json.dumps({"id": current_id, "type": kind, "text": str(text)}) + "\n")
    sys.__stdout__.flush()

class Wire:
    def __init__(self, kind): self.kind = kind
    def write(self, text):
        if text: emit(self.kind, text)
        return len(text)
    def flush(self): pass
    def isatty(self): return False

sys.stdout = Wire("stdout")
sys.stderr = Wire("stderr")

for raw in sys.__stdin__:
    try:
        request = json.loads(raw)
        current_id = request["id"]
        code = request.get("code", "")
        tree = ast.parse(code, mode="exec")
        if tree.body and isinstance(tree.body[-1], ast.Expr):
            prefix = ast.Module(body=tree.body[:-1], type_ignores=[])
            if prefix.body:
                exec(compile(prefix, "<notebook>", "exec"), namespace, namespace)
            value = eval(compile(ast.Expression(tree.body[-1].value), "<notebook>", "eval"), namespace, namespace)
            if value is not None:
                emit("result", repr(value))
        else:
            exec(compile(tree, "<notebook>", "exec"), namespace, namespace)
    except BaseException:
        emit("error", traceback.format_exc())
    finally:
        emit("done")
`
