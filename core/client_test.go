package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The app's controller client must reach mihomo over its Unix socket, so
// restarts do not depend on the TCP controller port being free.
func TestUnixClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets")
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("clashx-go-client-%d.sock", os.Getpid()))
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(sock)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta":true,"version":"test"}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	c := NewUnixClient(sock, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping over unix socket: %v", err)
	}
	v, err := c.Version(ctx)
	if err != nil || v != "test" {
		t.Fatalf("version = %q, %v", v, err)
	}
}
