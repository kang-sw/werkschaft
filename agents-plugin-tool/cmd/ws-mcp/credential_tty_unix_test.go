//go:build darwin || linux

package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func echoOn(t *testing.T, f *os.File) bool {
	t.Helper()
	tio, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	return tio.Lflag&unix.ECHO != 0
}

// TestCredentialKeyTypedAtATerminalIsNotEchoed: run directly on a terminal,
// `credential set` turns echo off before the key is typed and back on after.
func TestCredentialKeyTypedAtATerminalIsNotEchoed(t *testing.T) {
	master, slave := openPTY(t)
	if !echoOn(t, slave) {
		t.Fatal("the fresh terminal has echo off already")
	}
	var stderr bytes.Buffer
	cli := &hostedCLI{stdin: slave, stderr: &stderr}
	type read struct {
		key string
		err error
	}
	got := make(chan read, 1)
	go func() {
		key, err := cli.readKey()
		got <- read{key, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for echoOn(t, slave) {
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := unix.Write(master, []byte(credentialCLIFixtureKey+"\n")); err != nil {
		t.Fatal(err)
	}
	var r read
	select {
	case r = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the key typed at the terminal was never read")
	}
	if r.err != nil || r.key != credentialCLIFixtureKey {
		t.Fatalf("key = %q, %v", r.key, r.err)
	}
	if !echoOn(t, slave) {
		t.Fatal("echo was not turned back on")
	}
	if text := stderr.String(); !strings.Contains(text, "API key: ") || strings.Contains(text, credentialCLIFixtureKey) {
		t.Fatalf("stderr = %q", text)
	}
	// Nothing came back to the terminal's screen side: no echo.
	if err := unix.SetNonblock(master, true); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := unix.Read(master, buf)
	if n > 0 && strings.Contains(string(buf[:n]), "fixture") {
		t.Fatalf("the terminal echoed the key: %q", buf[:n])
	}
	if n <= 0 && err != nil && !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("read the terminal's screen side: %v", err)
	}
}

// TestCredentialKeyFromAPipeHasNoPrompt: a non-terminal stdin keeps the plain
// first-line read, with no prompt and no wait for EOF.
func TestCredentialKeyFromAPipeHasNoPrompt(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if _, err := pw.WriteString(credentialCLIFixtureKey + "\nsecond line\n"); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cli := &hostedCLI{stdin: pr, stderr: &stderr}
	got := make(chan string, 1)
	go func() {
		key, err := cli.readKey()
		if err != nil {
			key = "error: " + err.Error()
		}
		got <- key
	}()
	select {
	case key := <-got:
		if key != credentialCLIFixtureKey {
			t.Fatalf("key = %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a held-open pipe was read to EOF")
	}
	if stderr.Len() != 0 {
		t.Fatalf("a pipe got a prompt: %q", stderr.String())
	}
}
