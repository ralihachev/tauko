// Package llm runs Claude through the Claude Code CLI (`claude -p`), on the user's own
// plan: no API keys. Calls are lean: their own system prompt, no tools (Read only for
// files passed in), no MCP servers, no skills and no saved session, so a call costs a
// few thousand tokens instead of the ~100k a full session loads.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Func sends a system prompt and a prompt to a model and returns its text reply.
// files are paths the model may read; nil means no tools at all.
type Func func(ctx context.Context, system, prompt string, files []string) (string, error)

// Available reports whether the Claude Code CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// Claude returns a Func backed by `claude -p`. model "" uses Claude Code's default.
func Claude(model string) Func {
	return func(ctx context.Context, system, prompt string, files []string) (string, error) {
		args := []string{"-p", "--output-format", "json", "--system-prompt", system,
			"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"}
		if len(files) == 0 {
			args = append(args, "--tools", "")
		} else {
			args = append(args, "--tools", "Read")
			for _, f := range files {
				args = append(args, "--add-dir", filepath.Dir(f))
			}
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", args...)
		cmd.Stdin = strings.NewReader(prompt)
		cmd.Dir = os.TempDir()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		var env struct {
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			return "", fmt.Errorf("claude -p: unexpected output: %w", err)
		}
		if env.IsError {
			return "", fmt.Errorf("claude -p: %s", env.Result)
		}
		return env.Result, nil
	}
}
