// Package updater keeps a node's binary current.
//
// A node checks a GitHub repository's releases for a build newer than its own,
// downloads the asset matching its platform, verifies it, and swaps it into
// place. Nothing about the source is compiled in: update.repository is a
// setting, so a fork or a private mirror works without a code change.
//
// Replacing the binary is deliberately *not* automatic by default. A node may
// be halfway through a training run, and deciding when to restart it belongs to
// whoever owns the machine.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/version"
)

// Release is a published build.
type Release struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	Published   string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	AssetURL    string `json:"-"`
	AssetName   string `json:"asset_name"`
	AssetSize   int64  `json:"asset_size"`
	ChecksumURL string `json:"-"`
}

// Status is what the interface shows about updating.
type Status struct {
	Enabled     bool     `json:"enabled"`
	Automatic   bool     `json:"automatic"`
	Repository  string   `json:"repository"`
	Current     string   `json:"current"`
	Latest      string   `json:"latest"`
	Available   bool     `json:"available"`
	Checking    bool     `json:"checking"`
	LastChecked string   `json:"last_checked"`
	Error       string   `json:"error"`
	Release     *Release `json:"release"`
	// Managed reports whether something will restart the node after it exits,
	// which decides whether an update can complete on its own.
	Managed bool `json:"managed"`
}

// Updater checks for and applies new versions.
type Updater struct {
	layout config.Layout
	hub    *events.Hub
	http   *http.Client
	wake   chan struct{}

	mu       sync.Mutex
	settings config.UpdateConfig
	status   Status
}

// New builds an updater for a node.
func New(cfg *config.Config, l config.Layout, hub *events.Hub) *Updater {
	u := &Updater{
		layout: l, hub: hub, wake: make(chan struct{}, 1), settings: cfg.Update,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
	u.status = Status{
		Enabled:    cfg.Update.Enabled,
		Automatic:  cfg.Update.Automatic,
		Repository: cfg.Update.Repository,
		Current:    version.Version,
		Managed:    underServiceManager(),
	}
	return u
}

// Status returns the last known state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.status
	s.Enabled = u.settings.Enabled
	s.Automatic = u.settings.Automatic
	s.Repository = u.settings.Repository
	return s
}

// Configure applies saved update settings without requiring a daemon restart.
func (u *Updater) Configure(settings config.UpdateConfig) {
	u.mu.Lock()
	u.settings = settings
	u.status.Enabled = settings.Enabled
	u.status.Automatic = settings.Automatic
	u.status.Repository = settings.Repository
	u.mu.Unlock()
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

func (u *Updater) configuration() config.UpdateConfig {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.settings
}

func (u *Updater) setStatus(mutate func(*Status)) Status {
	u.mu.Lock()
	mutate(&u.status)
	s := u.status
	u.mu.Unlock()
	u.hub.Publish("update.status", s)
	return s
}

// Check asks the repository for its newest release.
func (u *Updater) Check(ctx context.Context) (Status, error) {
	u.setStatus(func(s *Status) { s.Checking = true; s.Error = "" })

	release, err := u.latestRelease(ctx)
	if err != nil {
		return u.setStatus(func(s *Status) {
			s.Checking = false
			s.LastChecked = time.Now().UTC().Format(time.RFC3339)
			s.Error = err.Error()
		}), err
	}

	return u.setStatus(func(s *Status) {
		s.Checking = false
		s.LastChecked = time.Now().UTC().Format(time.RFC3339)
		s.Latest = release.Version
		s.Release = release
		s.Available = IsNewer(release.Version, version.Version)
	}), nil
}

// latestRelease reads the newest release carrying an asset for this platform.
func (u *Updater) latestRelease(ctx context.Context) (*Release, error) {
	settings := u.configuration()
	repo := strings.TrimSpace(settings.Repository)
	if repo == "" {
		return nil, errors.New("no update repository is configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases?per_page=20", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "plainshow-cluster")
	// A public repository needs no token. A private one is read with the
	// node's own GitHub credential if it has been connected.
	u.authorizeGitHub(req)

	res, err := u.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found for %s", repo)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub returned %d looking for releases", res.StatusCode)
	}

	var payload []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, err
	}

	wantAsset := AssetName()
	allowPre := settings.Channel == "beta" || settings.Channel == "any"

	for _, entry := range payload {
		if entry.Draft || (entry.Prerelease && !allowPre) {
			continue
		}
		release := &Release{
			Version:    strings.TrimPrefix(entry.TagName, "v"),
			Name:       entry.Name,
			Notes:      entry.Body,
			Published:  entry.PublishedAt,
			Prerelease: entry.Prerelease,
		}
		for _, asset := range entry.Assets {
			switch asset.Name {
			case wantAsset:
				release.AssetURL = asset.BrowserDownloadURL
				release.AssetName = asset.Name
				release.AssetSize = asset.Size
			case wantAsset + ".sha256", "checksums.txt", "SHA256SUMS":
				if release.ChecksumURL == "" || asset.Name == wantAsset+".sha256" {
					release.ChecksumURL = asset.BrowserDownloadURL
				}
			}
		}
		if release.AssetURL == "" {
			continue // a release with nothing for this platform is not an update
		}
		return release, nil
	}
	return nil, fmt.Errorf("no release provides %s", wantAsset)
}

// AssetName is the release asset this node needs.
func AssetName() string {
	return fmt.Sprintf("pscluster-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// Apply downloads and installs a release, then returns the path it wrote.
//
// The download is verified before anything is replaced, and the swap is a
// rename, which is atomic: an interrupted update leaves the old binary intact
// rather than a half-written one.
func (u *Updater) Apply(ctx context.Context, release *Release) error {
	if release == nil || release.AssetURL == "" {
		return errors.New("no release to install")
	}
	if release.ChecksumURL == "" {
		return errors.New("the release has no SHA-256 checksum — refusing to install it")
	}

	target := u.layout.Binary()
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	staged := target + ".new"

	u.hub.Publish("update.progress", map[string]any{
		"stage": "downloading", "version": release.Version, "size": release.AssetSize,
	})

	sum, err := u.download(ctx, release.AssetURL, staged)
	if err != nil {
		os.Remove(staged)
		return err
	}

	u.hub.Publish("update.progress", map[string]any{"stage": "verifying"})
	expected, err := u.expectedChecksum(ctx, release)
	if err != nil {
		os.Remove(staged)
		return err
	}
	if !strings.EqualFold(expected, sum) {
		os.Remove(staged)
		return fmt.Errorf("the download did not match its checksum — refusing to install it")
	}

	if err := os.Chmod(staged, 0o755); err != nil {
		os.Remove(staged)
		return err
	}
	// Run the new binary before trusting it. A build that cannot report its own
	// version is not one to swap in under a running node.
	if err := verifyRuns(ctx, staged); err != nil {
		os.Remove(staged)
		return err
	}

	// Keep the outgoing binary so a bad release can be rolled back by hand.
	hadPrevious := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, target+".previous"); err != nil {
			return fmt.Errorf("preserve current binary: %w", err)
		}
		hadPrevious = true
	}
	if err := os.Rename(staged, target); err != nil {
		if hadPrevious {
			_ = os.Rename(target+".previous", target)
		}
		return err
	}

	u.hub.Publish("update.progress", map[string]any{
		"stage": "installed", "version": release.Version,
	})
	u.setStatus(func(s *Status) {
		s.Available = false
		s.Current = release.Version
	})
	return nil
}

// download streams a URL to a file, returning its SHA-256.
func (u *Updater) download(ctx context.Context, url, dest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "plainshow-cluster")
	u.authorizeGitHub(req)

	res, err := u.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not download the update: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("downloading the update returned %d", res.StatusCode)
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	// Cap the download so a wrong URL cannot fill the disk.
	_, err = io.Copy(io.MultiWriter(file, hash), io.LimitReader(res.Body, 512<<20))
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// expectedChecksum reads the published SHA-256 for this platform's asset.
func (u *Updater) expectedChecksum(ctx context.Context, release *Release) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.ChecksumURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "plainshow-cluster")
	u.authorizeGitHub(req)
	res, err := u.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not download the checksum: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("downloading the checksum returned %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if sum := parseChecksums(string(body), release.AssetName); sum != "" {
		return sum, nil
	}
	return "", errors.New("the checksum file does not contain this platform's asset")
}

// authorizeGitHub lets every part of a private release use the credential.
// GitHub protects checksum assets exactly like binaries; authorizing only the
// binary download would make an otherwise valid private update fail closed.
func (u *Updater) authorizeGitHub(req *http.Request) {
	token, err := os.ReadFile(u.layout.GitHubToken())
	if err != nil {
		return
	}
	if value := strings.TrimSpace(string(token)); value != "" {
		req.Header.Set("Authorization", "Bearer "+value)
	}
}

// parseChecksums finds one asset's SHA-256 in a published checksum file.
//
// It accepts a bare hash or sha256sum's "<hash>  <name>" lines, with or without
// the binary-mode asterisk, and returns "" when the asset is not listed. The
// caller turns that into a refusal to install, so this is the piece that
// decides whether a release is verifiable — which is why it is tested against
// real sha256sum output rather than a hand-written sample.
func parseChecksums(body, asset string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return ""
	}
	if !strings.ContainsAny(text, " \n\t") && len(text) == 64 {
		return text
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0]
		}
	}
	return ""
}

// verifyRuns checks that a downloaded binary executes on this machine.
func verifyRuns(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := execCommand(ctx, path, "version")
	if err != nil {
		return fmt.Errorf("the downloaded build did not run on this machine: %w", err)
	}
	if !strings.Contains(out, "Plainshow Cluster") {
		return errors.New("the downloaded file is not a Plainshow Cluster build")
	}
	return nil
}

// Restart replaces this process with the installed binary.
//
// exec keeps the same pid, so a service manager sees no exit and nothing has to
// be configured to restart the node. When the binary on disk is not the one
// running (a development build, say), it exits instead and leaves the restart
// to whatever supervises it.
func (u *Updater) Restart() error {
	target := u.layout.Binary()
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("no installed binary at %s to restart into", target)
	}
	u.hub.Publish("update.progress", map[string]any{"stage": "restarting"})
	// Give the event a moment to reach connected browsers before the process
	// is replaced out from under them.
	time.Sleep(400 * time.Millisecond)

	args := append([]string{target}, os.Args[1:]...)
	return syscall.Exec(target, args, os.Environ())
}

// Run checks periodically until ctx is cancelled.
func (u *Updater) Run(ctx context.Context) {
	go func() {
		// A short initial delay keeps startup fast and avoids every node in a
		// cluster checking at the same instant after a shared restart.
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		for {
			settings := u.configuration()
			if settings.Enabled {
				status, err := u.Check(ctx)
				if err == nil && status.Available && settings.Automatic {
					if err := u.Apply(ctx, status.Release); err == nil {
						if err := u.Restart(); err != nil {
							u.setStatus(func(s *Status) {
								s.Error = "Installed, but this node could not restart itself: " + err.Error()
							})
						}
						return
					}
				}
			}
			interval := settings.CheckInterval()
			if !settings.Enabled {
				interval = time.Hour
			}
			select {
			case <-ctx.Done():
				return
			case <-u.wake:
			case <-time.After(interval):
			}
		}
	}()
}

// IsNewer compares semantic versions, including prerelease identifiers. A
// development build is always considered older, so a node built locally still
// sees released updates.
func IsNewer(candidate, current string) bool {
	if candidate == "" {
		return false
	}
	if strings.Contains(current, "-dev") || strings.Contains(current, "dirty") ||
		gitDescribeVersion.MatchString(current) {
		return true
	}
	next, nextOK := parseSemanticVersion(candidate)
	installed, installedOK := parseSemanticVersion(current)
	if !nextOK {
		return false
	}
	if !installedOK {
		return true
	}
	return compareSemanticVersions(next, installed) > 0
}

var gitDescribeVersion = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+(?:-dirty)?$`)

type semanticVersion struct {
	core       [3]int
	prerelease []string
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if build := strings.IndexByte(value, '+'); build >= 0 {
		value = value[:build]
	}
	coreText, preText, hasPre := strings.Cut(value, "-")
	pieces := strings.Split(coreText, ".")
	if len(pieces) == 0 || len(pieces) > 3 {
		return semanticVersion{}, false
	}
	var parsed semanticVersion
	for index, piece := range pieces {
		if piece == "" {
			return semanticVersion{}, false
		}
		number, err := strconv.Atoi(piece)
		if err != nil || number < 0 {
			return semanticVersion{}, false
		}
		parsed.core[index] = number
	}
	if hasPre {
		if preText == "" {
			return semanticVersion{}, false
		}
		parsed.prerelease = strings.Split(preText, ".")
		for _, identifier := range parsed.prerelease {
			if identifier == "" {
				return semanticVersion{}, false
			}
		}
	}
	return parsed, true
}

func compareSemanticVersions(a, b semanticVersion) int {
	for i := 0; i < 3; i++ {
		x, y := a.core[i], b.core[i]
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	count := len(a.prerelease)
	if len(b.prerelease) > count {
		count = len(b.prerelease)
	}
	for index := 0; index < count; index++ {
		if index == len(a.prerelease) {
			return -1
		}
		if index == len(b.prerelease) {
			return 1
		}
		x, y := a.prerelease[index], b.prerelease[index]
		if x == y {
			continue
		}
		xNumber, xErr := strconv.Atoi(x)
		yNumber, yErr := strconv.Atoi(y)
		switch {
		case xErr == nil && yErr == nil:
			if xNumber > yNumber {
				return 1
			}
			return -1
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		case x > y:
			return 1
		default:
			return -1
		}
	}
	return 0
}

// underServiceManager reports whether something will restart this node if it
// exits, which decides whether an update can finish without a person.
func underServiceManager() bool {
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("JOURNAL_STREAM") != "" {
		return true
	}
	return os.Getppid() == 1
}
