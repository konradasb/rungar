// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

const runnerImage = "ghcr.io/actions/actions-runner:latest"

func TestCreateMakesAndStartsTheContainer(t *testing.T) {
	e := newEngine(t)
	e.on(http.MethodGet, "/images/"+runnerImage+"/json", http.StatusOK, `{}`)
	e.on(http.MethodPost, "/containers/create", http.StatusCreated, `{"Id":"abc"}`)
	p := e.provider("")

	spec := runner(t, `
image: `+runnerImage+`
cpus: 2
memory: 4GiB
network: runners
env: {HTTPS_PROXY: "http://proxy:3128", ACTIONS_RUNNER_INPUT_JITCONFIG: overridden}
mounts:
  - {type: volume, source: cache, target: /home/runner/.cache}
  - {type: bind, source: /etc/ssl/certs, target: /etc/ssl/certs}
`)
	if err := p.Create(context.Background(), machine("rungar-c2-m4-1ff1015a", spec)); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if pulls := e.sent(http.MethodPost, "/images/create"); len(pulls) != 0 {
		t.Errorf("pulled %d times, want none: the host has the image", len(pulls))
	}

	creates := e.sent(http.MethodPost, "/containers/create")
	if len(creates) != 1 {
		t.Fatalf("created %d containers, want 1", len(creates))
	}
	if name := creates[0].query.Get("name"); name != "rungar-c2-m4-1ff1015a" {
		t.Errorf("container named %q, want the runner's name", name)
	}
	if strings.Contains(creates[0].body, "Privileged") {
		t.Errorf("create body mentions Privileged: %s", creates[0].body)
	}

	var got containerConfig
	decode(t, creates[0].body, &got)

	want := containerConfig{
		Image: runnerImage,
		Cmd:   []string{"/home/runner/run.sh"},
		Env: []string{
			"ACTIONS_RUNNER_INPUT_JITCONFIG=jit-rungar-c2-m4-1ff1015a",
			"HTTPS_PROXY=http://proxy:3128",
		},
		Labels: map[string]string{"rungar.sh/managed": "true", "rungar.sh/runner": "rungar-c2-m4-1ff1015a"},
		HostConfig: hostConfig{
			AutoRemove:  true,
			Init:        true,
			NanoCPUs:    2e9,
			Memory:      4 << 30,
			NetworkMode: "runners",
			Mounts: []apiMount{
				{Type: "volume", Source: "cache", Target: "/home/runner/.cache"},
				{Type: "bind", Source: "/etc/ssl/certs", Target: "/etc/ssl/certs", ReadOnly: true},
			},
		},
	}
	if !equalConfig(got, want) {
		t.Errorf("create body = %+v, want %+v", got, want)
	}

	if starts := e.sent(http.MethodPost, "/containers/rungar-c2-m4-1ff1015a/start"); len(starts) != 1 {
		t.Errorf("started %d times, want 1", len(starts))
	}
}

// equalConfig compares two create bodies.
func equalConfig(a, b containerConfig) bool {
	return a.Image == b.Image && slices.Equal(a.Cmd, b.Cmd) && slices.Equal(a.Env, b.Env) &&
		len(a.Labels) == len(b.Labels) && a.Labels["rungar.sh/runner"] == b.Labels["rungar.sh/runner"] &&
		a.HostConfig.AutoRemove == b.HostConfig.AutoRemove && a.HostConfig.Init == b.HostConfig.Init &&
		a.HostConfig.NanoCPUs == b.HostConfig.NanoCPUs && a.HostConfig.Memory == b.HostConfig.Memory &&
		a.HostConfig.NetworkMode == b.HostConfig.NetworkMode && slices.Equal(a.HostConfig.Mounts, b.HostConfig.Mounts)
}

func TestCreatePullsAMissingImage(t *testing.T) {
	e := newEngine(t)
	e.on(http.MethodGet, "/images/"+runnerImage+"/json", http.StatusNotFound, `{"message":"No such image"}`)
	e.on(http.MethodPost, "/images/create", http.StatusOK, `{"status":"Pulling"}`+"\n"+`{"status":"Done"}`)
	p := e.provider("")

	if err := p.Create(context.Background(), machine("r", runner(t, "image: "+runnerImage))); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	pulls := e.sent(http.MethodPost, "/images/create")
	if len(pulls) != 1 {
		t.Fatalf("pulled %d times, want 1", len(pulls))
	}
	if q := pulls[0].query; q.Get("fromImage") != "ghcr.io/actions/actions-runner" || q.Get("tag") != "latest" {
		t.Errorf("pulled %v, want the repository and its tag apart", q)
	}
}

func TestCreatePullsEveryTimeWhenAsked(t *testing.T) {
	e := newEngine(t)
	e.on(http.MethodPost, "/images/create", http.StatusOK, `{"status":"Done"}`)
	p := e.provider("")

	spec := runner(t, "image: alpine:3.20\npull: always")
	if err := p.Create(context.Background(), machine("r", spec)); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if inspects := e.sent(http.MethodGet, "/images/alpine:3.20/json"); len(inspects) != 0 {
		t.Error("asked whether the host has the image, which pull: always does not care")
	}
	if pulls := e.sent(http.MethodPost, "/images/create"); len(pulls) != 1 {
		t.Errorf("pulled %d times, want 1", len(pulls))
	}
}

// TestCreateSaysWhyItRefused checks each failure is put in the class that says
// what Rungar does next.
func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name  string
		setup func(e *engine)
		want  string // "full", "invalid", or "" for neither
	}{
		{"image not in the registry", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK,
				`{"status":"Pulling"}`+"\n"+`{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}`)
		}, "invalid"},
		{"image refused by the registry", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusNotFound,
				`{"message":"pull access denied for runner, repository does not exist"}`)
		}, "invalid"},
		{"out of disk pulling", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK,
				`{"error":"write /var/lib/docker/tmp/x: no space left on device"}`)
		}, "full"},
		{"registry unreachable", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusInternalServerError,
				`{"message":"Get https://ghcr.io/v2/: dial tcp: i/o timeout"}`)
		}, ""},
		{"unknown network", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/create", http.StatusNotFound, `{"message":"network runners not found"}`)
		}, "invalid"},
		{"invalid configuration", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/create", http.StatusBadRequest,
				`{"message":"invalid mount config for type \"bind\": bind source path does not exist"}`)
		}, "invalid"},
		{"name taken", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/create", http.StatusConflict, `{"message":"Conflict. The name is in use"}`)
		}, ""},
		{"out of disk starting", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/r/start", http.StatusInternalServerError,
				`{"message":"mkdir /var/lib/docker/overlay2/x: no space left on device"}`)
		}, "full"},
		{"volume that will not mount", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/r/start", http.StatusInternalServerError,
				`{"message":"error while mounting volume '/var/lib/docker/volumes/cache/_data': permission denied"}`)
		}, "invalid"},
		{"daemon failing", func(e *engine) {
			e.on(http.MethodPost, "/images/create", http.StatusOK, `{}`)
			e.on(http.MethodPost, "/containers/r/start", http.StatusInternalServerError, `{"message":"OCI runtime error"}`)
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEngine(t)
			tt.setup(e)
			p := e.provider("")

			err := p.Create(context.Background(), machine("r", runner(t, "image: runner\npull: always")))
			if err == nil {
				t.Fatal("Create() = nil, want an error")
			}

			got := ""
			switch {
			case errors.Is(err, errdefs.ErrNoCapacity):
				got = "full"
			case errors.Is(err, errdefs.ErrInvalidArgument):
				got = "invalid"
			}
			if got != tt.want {
				t.Errorf("Create() = %v, classed %q, want %q", err, got, tt.want)
			}
		})
	}
}

func TestCreateRefusesAnotherTypesRunner(t *testing.T) {
	p := newEngine(t).provider("")

	err := p.Create(context.Background(), machine("r", otherRunner{}))
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
}

type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }

func TestListFiltersByLabel(t *testing.T) {
	e := newEngine(t)
	e.on(http.MethodGet, "/containers/json", http.StatusOK, `[
		{"Names":["/rungar-a"],"Labels":{"rungar.sh/runner":"rungar-a"},"State":"running","Created":1790000000},
		{"Names":["/rungar-b"],"Labels":{"rungar.sh/runner":"rungar-b"},"State":"created","Created":1790000001},
		{"Names":["/rungar-c"],"Labels":{"rungar.sh/runner":"rungar-c"},"State":"exited","Created":1790000002}
	]`)
	p := e.provider("")

	machines, err := p.List(context.Background(), map[string]string{"rungar.sh/managed": "true", "rungar.sh/installation": "i1"})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	q := e.sent(http.MethodGet, "/containers/json")[0].query
	if q.Get("all") != "1" {
		t.Error("listed running containers alone; a stopped one must be seen too")
	}
	var filters map[string][]string
	decode(t, q.Get("filters"), &filters)
	if want := []string{"rungar.sh/installation=i1", "rungar.sh/managed=true"}; !slices.Equal(filters["label"], want) {
		t.Errorf("filters = %v, want labels %v", filters, want)
	}

	want := []types.Machine{
		{Name: "rungar-a", State: types.MachineRunning, CreatedAt: time.Unix(1790000000, 0)},
		{Name: "rungar-b", State: types.MachineStarting, CreatedAt: time.Unix(1790000001, 0)},
		{Name: "rungar-c", State: types.MachineStopped, CreatedAt: time.Unix(1790000002, 0)},
	}
	if len(machines) != len(want) {
		t.Fatalf("List() = %d machines, want %d", len(machines), len(want))
	}
	for i, m := range machines {
		w := want[i]
		if m.Name != w.Name || m.State != w.State || !m.CreatedAt.Equal(w.CreatedAt) || m.Labels["rungar.sh/runner"] != w.Name {
			t.Errorf("machine %d = %+v, want %+v", i, m, w)
		}
	}
}

func TestListIsBoundedByTheTimeout(t *testing.T) {
	e := newEngine(t)
	e.hang(http.MethodGet, "/containers/json", 5*time.Second)
	p := e.provider("timeout: 50ms")

	start := time.Now()
	if _, err := p.List(context.Background(), nil); err == nil {
		t.Fatal("List() = nil, want the timeout")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("List() took %v, want it bounded by the timeout", took)
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"removed", http.StatusNoContent, "", false},
		{"not there", http.StatusNotFound, `{"message":"No such container: r"}`, false},
		{"removing itself", http.StatusConflict, `{"message":"removal of container r is already in progress"}`, false},
		{"failing", http.StatusInternalServerError, `{"message":"driver failed"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEngine(t)
			e.on(http.MethodDelete, "/containers/r", tt.status, tt.body)
			p := e.provider("")

			err := p.Delete(context.Background(), "r")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Delete() = %v, want an error: %v", err, tt.wantErr)
			}

			q := e.sent(http.MethodDelete, "/containers/r")[0].query
			if q.Get("force") != "1" || q.Get("v") != "1" {
				t.Errorf("removed with %v, want force and its anonymous volumes", q)
			}
		})
	}
}

// TestSocketClient checks the client reaches a daemon on its unix socket.
func TestSocketClient(t *testing.T) {
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "d.sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+apiVersion+"/containers/json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"Names":["/r"],"Labels":{"a":"b"},"State":"running"}]`))
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	p := open(t, configure(t, "address: unix://"+path))

	machines, err := p.List(context.Background(), nil)
	if err != nil || len(machines) != 1 || machines[0].Name != "r" {
		t.Errorf("List() = %+v, %v; want the daemon's one container", machines, err)
	}
}
