package metadata

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommandOutputHelper(t *testing.T) {
	switch os.Getenv("ANIME_RENAMER_COMMAND_TEST") {
	case "large":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), maxProbeOutput+1))
		os.Exit(0)
	case "sleep":
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
}

func TestCommandBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"large", "sleep"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ANIME_RENAMER_COMMAND_TEST", mode)
			timeout := 5 * time.Second
			if mode == "sleep" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, err := runContext(ctx, os.Args[0], "-test.run=^TestCommandOutputHelper$")
			if mode == "large" && (err == nil || !strings.Contains(err.Error(), "output exceeds")) {
				t.Fatalf("expected output limit: %v", err)
			}
			if mode == "sleep" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected timeout: %v", err)
			}
		})
	}
}
