package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const DefaultTimeout = 30 * time.Minute
const maxProbeOutput = 8 << 20

type EmbedOptions struct {
	KeepBackup bool
	Timeout    time.Duration
	Context    context.Context
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *limitedBuffer) Len() int       { return b.buffer.Len() }
func (b *limitedBuffer) String() string { return b.buffer.String() }
func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	limit := b.limit
	if limit == 0 {
		limit = 16 << 10
	}
	remaining := max(0, limit-b.Len())
	_, _ = b.buffer.Write(p[:min(n, remaining)])
	if n > remaining {
		b.overflow = true
		if b.cancel != nil {
			b.cancel()
		}
	}
	return n, nil
}

func run(program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return runContext(ctx, program, args...)
}

func runContext(ctx context.Context, program string, args ...string) ([]byte, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, program, args...)
	cmd.WaitDelay = 2 * time.Second
	stdout := limitedBuffer{limit: maxProbeOutput, cancel: cancel}
	var stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.overflow {
		return nil, fmt.Errorf("%s output exceeds %d bytes", program, maxProbeOutput)
	}
	if err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if stderr.overflow {
			diagnostic += " [truncated]"
		}
		return nil, fmt.Errorf("%s: %w: %s", program, errors.Join(err, ctx.Err()), diagnostic)
	}
	return stdout.Bytes(), nil
}

func packetHash(ctx context.Context, path string, streams []probeStream) ([]byte, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Explicit stream maps include every original stream, omitting only an
	// independently verified generated chapter track. Hash output is streamed.
	args := []string{"-nostdin", "-v", "error", "-copyts", "-protocol_whitelist", "file,pipe", "-i", path}
	for _, stream := range streams {
		args = append(args, "-map", fmt.Sprintf("0:%d", stream.Index))
	}
	args = append(args, "-c", "copy", "-avoid_negative_ts", "disabled", "-f", "framehash", "-hash", "sha256", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.WaitDelay = 2 * time.Second
	digest := sha256.New()
	var stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = digest, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("verify copied packets: %w: %s", errors.Join(err, ctx.Err()), strings.TrimSpace(stderr.String()))
	}
	return digest.Sum(nil), nil
}

func readProbe(path string) (*probe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return readProbeContext(ctx, path)
}

func readProbeContext(ctx context.Context, path string) (*probe, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := runContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-show_streams", "-show_format", "-show_chapters", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	var p probe
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	tags := make(map[string]string)
	for key, value := range p.Format.Tags {
		tags[strings.ToLower(key)] = value
	}
	p.Format.Tags = tags
	return &p, nil
}
