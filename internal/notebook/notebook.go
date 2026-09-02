// Package notebook drives an installed Jupyter Server. Plainshow owns the
// workspace experience and reverse proxy; Jupyter owns kernels, rich output,
// completion, inspection and widgets.
package notebook

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var ErrNoJupyter = errors.New("jupyter_server is not installed; install it in the Python environment you want Plainshow to use")

type server struct {
	id, dir, token string
	port           int
	cmd            *exec.Cmd
	started        time.Time
}

type Status struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Started   string `json:"started_at"`
	URL       string `json:"url,omitempty"`
}

type Manager struct {
	mu    sync.Mutex
	byDir map[string]*server
	byID  map[string]*server
}

func NewManager() *Manager { return &Manager{byDir: map[string]*server{}, byID: map[string]*server{}} }

func command() (string, []string, error) {
	if path, err := exec.LookPath("jupyter-server"); err == nil {
		return path, nil, nil
	}
	if path, err := exec.LookPath("jupyter"); err == nil {
		return path, []string{"server"}, nil
	}
	return "", nil, ErrNoJupyter
}

func Available() bool { _, _, err := command(); return err == nil }

func (m *Manager) Status(projectDir string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{Available: Available()}
	if item := m.byDir[projectDir]; item != nil && item.cmd.ProcessState == nil {
		return m.statusLocked(item)
	}
	return status
}

func (m *Manager) Open(projectDir string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.byDir[projectDir]; item != nil && item.cmd.ProcessState == nil {
		return m.statusLocked(item), nil
	}
	program, prefix, err := command()
	if err != nil {
		return Status{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Status{}, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	random, idBytes := make([]byte, 24), make([]byte, 9)
	if _, err := rand.Read(random); err != nil {
		return Status{}, err
	}
	if _, err := rand.Read(idBytes); err != nil {
		return Status{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	token := base64.RawURLEncoding.EncodeToString(random)
	base := "/jupyter/" + id + "/"
	args := append(prefix, "--no-browser", "--ip=127.0.0.1", "--port="+strconv.Itoa(port),
		"--ServerApp.root_dir="+projectDir, "--ServerApp.base_url="+base,
		"--ServerApp.token="+token, "--ServerApp.password=", "--ServerApp.allow_remote_access=False")
	cmd := exec.Command(program, args...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "JUPYTER_PLATFORM_DIRS=1")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Status{}, fmt.Errorf("start jupyter_server: %w", err)
	}
	item := &server{id: id, dir: projectDir, token: token, port: port, cmd: cmd, started: time.Now()}
	m.byDir[projectDir], m.byID[id] = item, item
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if m.byDir[projectDir] == item {
			delete(m.byDir, projectDir)
			delete(m.byID, id)
		}
		m.mu.Unlock()
	}()
	return m.statusLocked(item), nil
}

func (m *Manager) statusLocked(item *server) Status {
	return Status{Available: true, Running: true, PID: item.cmd.Process.Pid,
		Started: item.started.UTC().Format(time.RFC3339),
		URL:     "/jupyter/" + item.id + "/tree?token=" + url.QueryEscape(item.token)}
}

func (m *Manager) Proxy(id string) (*httputil.ReverseProxy, error) {
	m.mu.Lock()
	item := m.byID[id]
	m.mu.Unlock()
	if item == nil || item.cmd.ProcessState != nil {
		return nil, errors.New("that Jupyter server is not running")
	}
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(item.port))
	return httputil.NewSingleHostReverseProxy(target), nil
}

func (m *Manager) Restart(projectDir string) error {
	m.mu.Lock()
	item := m.byDir[projectDir]
	m.mu.Unlock()
	if item == nil {
		return nil
	}
	return syscall.Kill(-item.cmd.Process.Pid, syscall.SIGTERM)
}

func (m *Manager) Close() {
	m.mu.Lock()
	items := make([]*server, 0, len(m.byDir))
	for _, item := range m.byDir {
		items = append(items, item)
	}
	m.mu.Unlock()
	for _, item := range items {
		_ = syscall.Kill(-item.cmd.Process.Pid, syscall.SIGTERM)
	}
}
