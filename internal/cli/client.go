// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/konradasb/rungar/internal/config"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// socketEnv is the environment variable naming the daemon's socket.
const socketEnv = "RUNGAR_SOCKET"

// socketPath returns the daemon's socket: --socket, else $RUNGAR_SOCKET, else
// the default.
func socketPath(cmd *cobra.Command) string {
	if path, _ := cmd.Flags().GetString("socket"); path != "" {
		return path
	}
	if path := os.Getenv(socketEnv); path != "" {
		return path
	}

	return config.DefaultSocket
}

// newClient returns a client of the daemon on the command's socket, and a
// function that closes it. It dials the socket first, so that a daemon not
// running, or a socket the caller may not use, is reported plainly.
func newClient(cmd *cobra.Command) (rungarv1.RungarServiceClient, func(), error) {
	ctx, path := cmd.Context(), socketPath(cmd)

	switch _, err := os.Stat(path); {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, fmt.Errorf("there is no socket at %s: is rungar running? "+
			"--socket or %s says where it is if it is somewhere else", path, socketEnv)
	case errors.Is(err, fs.ErrPermission):
		return nil, nil, fmt.Errorf("the socket at %s cannot be reached: %w; "+
			"run this as a member of the rungar group, or as root", path, err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", path)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return nil, nil, fmt.Errorf("the socket at %s cannot be used: permission denied; "+
			"run this as a member of the rungar group, or as root", path)
	case err != nil:
		return nil, nil, fmt.Errorf("rungar is not answering on %s: is it running? %w", path, err)
	}
	_ = conn.Close()

	cc, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", path, err)
	}

	return rungarv1.NewRungarServiceClient(cc), func() { _ = cc.Close() }, nil
}

// withClient runs run with a client of the daemon, then closes it.
func withClient(cmd *cobra.Command, run func(rungarv1.RungarServiceClient) error) error {
	client, closeConn, err := newClient(cmd)
	if err != nil {
		return err
	}
	defer closeConn()

	return run(client)
}
