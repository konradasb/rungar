// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// host is the machine under test, reached with the ssh and scp binaries so
// the user's SSH configuration (agents, jump hosts, keys) applies.
type host struct {
	addr string
	user string
	key  string
}

// sshArgs returns the options every ssh and scp call takes. BatchMode fails
// instead of prompting for a passphrase.
func (h *host) sshArgs() []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
	}
	if h.key != "" {
		args = append(args, "-i", h.key)
	}

	return args
}

// target returns user@address.
func (h *host) target() string {
	return h.user + "@" + h.addr
}

// run runs argv on the host and returns its combined output. ssh passes one
// command line to a remote shell, so each argument is quoted.
func (h *host) run(ctx context.Context, argv ...string) (string, error) {
	args := append(h.sshArgs(), h.target(), quoteCommand(argv))

	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s",
			strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// runShell runs a shell script on the host, for pipelines and redirects. The
// caller quotes it.
func (h *host) runShell(ctx context.Context, script string) (string, error) {
	args := append(h.sshArgs(), h.target(), script)

	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", script, err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// uploadExecutable copies a local file to the host and makes it executable.
func (h *host) uploadExecutable(ctx context.Context, localPath, remotePath string) error {
	args := append(h.sshArgs(), localPath, h.target()+":"+remotePath)

	if out, err := exec.CommandContext(ctx, "scp", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("upload %s: %w: %s", localPath, err, strings.TrimSpace(string(out)))
	}

	if _, err := h.run(ctx, "chmod", "+x", remotePath); err != nil {
		return fmt.Errorf("chmod %s: %w", remotePath, err)
	}

	return nil
}

// arch returns the host's architecture as a GOARCH.
func (h *host) arch(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "uname", "-m")
	if err != nil {
		return "", err
	}

	switch machine := strings.TrimSpace(out); machine {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported host architecture %q", machine)
	}
}

// quoteCommand returns argv as one shell command line, each argument
// single-quoted.
func quoteCommand(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}

	return strings.Join(quoted, " ")
}
