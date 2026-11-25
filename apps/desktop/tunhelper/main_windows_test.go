package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Exercise only the unprivileged rendezvous, with the test process as peer.
// This does not call serve, load Wintun, request elevation or configure a NIC.
func TestLocalPipePeer(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(key[:])
	pid := windows.GetCurrentProcessId()
	args := []string{"full", strconv.FormatUint(uint64(pid), 10), nonce}
	if _, _, _, err := helperArgs(args); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{
		{"full", "0", nonce}, {"full", "10", strings.Repeat("g", 64)},
		{"other", "10", nonce}, {"test", "10", nonce, "extra"},
	} {
		if _, _, _, err := helperArgs(bad); err == nil {
			t.Fatal("accepted invalid arguments")
		}
	}
	name, _ := windows.UTF16PtrFromString(`\\.\pipe\NexusTun-` + nonce)
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)
	client, err := openLocalPipe(nonce)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(client)
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(proc)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := verifyPeer(client, proc, pid, exe); err != nil || owner == "" {
		t.Fatalf("same process rejected: %v", err)
	}
	if _, err := verifyPeer(client, proc, pid+1, exe); err == nil {
		t.Fatal("accepted incorrect pipe owner")
	}
	if _, err := verifyPeer(client, proc, pid, filepath.Join(t.TempDir(), "nexus-core.exe")); err == nil {
		t.Fatal("accepted a different installation")
	}
}
