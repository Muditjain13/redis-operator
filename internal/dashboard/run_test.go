/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dashboard

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMainRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--nope"}},
		{"stray argument", []string{"reset-everything"}},
		{"help", []string{"-h"}},
		{"bad duration", []string{"--cooldown=soon"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Main(tt.args, nil, &out, &errOut); code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
		})
	}
}

func TestMainFailsWithoutKubernetesAccess(t *testing.T) {
	// Point every place a kubeconfig could come from at an empty directory, so
	// the test cannot pick up the developer's real cluster.
	empty := t.TempDir()
	t.Setenv("KUBECONFIG", empty+"/none")
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	var out, errOut bytes.Buffer
	if code := Main([]string{"--addr=127.0.0.1:0"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s%s", code, out.String(), errOut.String())
	}
}

func TestServeAnswersAndStopsGracefully(t *testing.T) {
	app := New(Config{}, newFakeBackend())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, l) }()

	resp, err := http.Get("http://" + l.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a clean shutdown must not be an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context ended")
	}
}

func TestServeEndsOpenStreamsOnShutdown(t *testing.T) {
	app := New(Config{}, newFakeBackend())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, l) }()

	stream := openSSE(t, "http://"+l.Addr().String()+"/api/stream")
	stream.next(t) // an open, idle stream

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown with an open stream failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an open event stream blocked shutdown")
	}
}

func TestServeReportsAListenerFailure(t *testing.T) {
	app := New(Config{}, newFakeBackend())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close() // serving on a closed listener fails immediately

	if err := app.Serve(context.Background(), l); err == nil {
		t.Fatal("expected an error")
	}
}
