package worker

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/langs"
)

// ProbeToolchains runs every language's version_command on this machine
// and returns the first line each prints, by language id ("" when the
// command is missing or fails: the toolchain is not installed here). The
// commands are the administrator's, not contestants', so they run outside
// the sandbox, a few at a time and with a short deadline.
func ProbeToolchains(ctx context.Context, reg *langs.Registry) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, l := range reg.All() {
		if len(l.VersionCommand) == 0 {
			continue
		}
		wg.Add(1)
		go func(l *langs.Language) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v := toolchainVersion(ctx, l.VersionCommand)
			mu.Lock()
			out[l.ID] = v
			mu.Unlock()
		}(l)
	}
	wg.Wait()
	return out
}

func toolchainVersion(ctx context.Context, argv []string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	// Some print their version on standard error (java -version).
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return ""
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			if len(line) > 160 {
				line = line[:160]
			}
			return line
		}
	}
	return ""
}
