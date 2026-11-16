package client

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

//go:embed bridge.mjs
var bridge string

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Event struct {
	Type     string  `json:"type"`
	Text     string  `json:"text"`
	Model    string  `json:"model"`
	Provider string  `json:"provider"`
	Error    string  `json:"error"`
	Models   []Model `json:"models"`
}

// POC delegates gateway startup, authentication and free routing to the existing Node client.
type POC struct {
	dir, node string
	gate      chan struct{}
	mu        sync.Mutex
	closed    bool
	workers   sync.WaitGroup
}

func NewPOC(dir string) (*POC, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("Node.js 22 or later is required for the POC gateway")
	}
	candidates := []string{dir}
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		candidates = []string{filepath.Join(cwd, "POC"), filepath.Join(cwd, "..", "POC"),
			filepath.Join(filepath.Dir(executable), "..", "POC")}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "src", "client.mjs")); err == nil && !info.IsDir() {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return nil, err
			}
			return &POC{dir: absolute, node: node, gate: make(chan struct{}, 1)}, nil
		}
	}
	return nil, errors.New("POC source not found; pass --poc-dir /path/to/freeway-cli/POC")
}

// Close prevents new workers and waits for current workers after their contexts are cancelled.
func (p *POC) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.workers.Wait()
}

func (p *POC) Models(ctx context.Context) ([]Model, error) {
	var models []Model
	err := p.run(ctx, map[string]any{"action": "models"}, func(event Event) {
		if event.Type == "models" {
			models = event.Models
		}
	})
	if err == nil && len(models) == 0 {
		err = errors.New("POC returned no free models")
	}
	return models, err
}

func (p *POC) Stream(ctx context.Context, model string, messages []Message, emit func(Event)) error {
	return p.run(ctx, map[string]any{"action": "chat", "model": model, "messages": messages}, emit)
}

func (p *POC) run(ctx context.Context, request any, emit func(Event)) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("POC adapter is closed")
	}
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	// Each Node client can bootstrap the shared POC; never bootstrap twice at once.
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.gate }()

	input, err := json.Marshal(request)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.node, "--input-type=module", "--eval", bridge, p.dir)
	cmd.Dir = p.dir
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	var protocolError error
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			protocolError = fmt.Errorf("invalid POC bridge event: %w", err)
			_ = cmd.Process.Kill()
			break
		}
		if event.Type == "error" {
			protocolError = errors.New(event.Error)
		} else {
			emit(event)
		}
	}
	if scanner.Err() != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if protocolError != nil {
		return protocolError
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("POC bridge failed: %w: %s", waitErr, stderr.String())
	}
	return nil
}
