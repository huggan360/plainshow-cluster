package gitrepo

// Cloning with something to show for it.
//
// A clone of a real repository takes long enough that a spinner is a lie: it
// says "working" when what somebody wants to know is "how much longer". Git
// already reports its own progress; this reads it rather than inventing one.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/projectfs"
)

// Progress is one observation of a running clone.
type Progress struct {
	// Phase is git's own word for what it is doing: "Receiving objects",
	// "Resolving deltas", and so on.
	Phase string `json:"phase"`
	// Percent is 0-100 within the current phase, or -1 when git is doing
	// something it does not measure.
	Percent int `json:"percent"`
}

// gitProgress matches the counter git writes to stderr, e.g.
// "Receiving objects:  73% (1234/1690), 4.21 MiB | 2.10 MiB/s".
var gitProgress = regexp.MustCompile(`^([A-Za-z][A-Za-z ]+):\s+(\d+)%`)

// CloneWithProgress clones a repository, reporting as it goes.
//
// Git writes progress to stderr, in carriage-return-separated updates rather
// than lines, which is why the scanner splits on both. Without --progress git
// stays silent when its output is not a terminal — which it never is here.
func CloneWithProgress(ctx context.Context, repository, destination, token string,
	onProgress func(Progress)) error {

	if !Available() {
		return ErrNoGit
	}
	url := "https://github.com/" + repository + ".git"
	helper := `!f() { echo username=x-access-token; echo "password=$PSCLUSTER_GIT_TOKEN"; }; f`
	args := []string{"-c", "credential.helper=", "-c", "credential.helper=" + helper,
		"clone", "--progress", "--", url, destination}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"PSCLUSTER_GIT_TOKEN="+token,
	)
	projectfs.AsOwner(cmd, destination)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var tail strings.Builder
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanLinesOrReturns)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Keep the last of it for the error message: git says why it failed on
		// stderr too, and the progress reader would otherwise swallow it.
		tail.Reset()
		tail.WriteString(line)
		if match := gitProgress.FindStringSubmatch(line); match != nil && onProgress != nil {
			percent, _ := strconv.Atoi(match[2])
			onProgress(Progress{Phase: strings.TrimSpace(match[1]), Percent: percent})
		}
	}
	if err := cmd.Wait(); err != nil {
		message := tail.String()
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("clone %s: %s", repository, message)
	}
	return nil
}

// scanLinesOrReturns splits on \n and \r, because git redraws its progress
// counter with a carriage return rather than starting a new line.
func scanLinesOrReturns(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for index, character := range data {
		if character == '\n' || character == '\r' {
			return index + 1, data[:index], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
