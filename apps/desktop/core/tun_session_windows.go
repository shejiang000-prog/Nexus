package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/xtls/libxray/tuntrust"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Set by the paired build, before constructing the ordinary-privilege core.
var tunHelperSHA256 string

type tunFailure string

func (f tunFailure) Error() string { return string(f) }

func tunErrorCode(err error) string {
	var f tunFailure
	if errors.As(err, &f) {
		return string(f)
	}
	return ""
}

type shellExecuteInfo struct {
	Size, Mask                uint32
	Window                    windows.Handle
	Verb, File, Args, WorkDir *uint16
	Show                      int32
	Instance                  windows.Handle
	IDList                    uintptr
	Class                     *uint16
	ClassKey                  windows.Handle
	HotKey                    uint32
	Icon, Process             windows.Handle
}

func launchTunHelper(path, mode, nonce string) (windows.Handle, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exe, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(path))
	args, _ := windows.UTF16PtrFromString(mode + " " + strconv.Itoa(os.Getpid()) + " " + nonce)
	// SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC | SEE_MASK_FLAG_NO_UI.
	info := shellExecuteInfo{Mask: 0x40 | 0x100 | 0x400, Verb: verb, File: exe, Args: args, WorkDir: dir}
	info.Size = uint32(unsafe.Sizeof(info))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}
	call := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	ok, _, e := call.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if ok == 0 || info.Process == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) || errors.Is(e, windows.ERROR_ACCESS_DENIED) {
			return 0, tunFailure("tun_admin")
		}
		return 0, tunFailure("tun_driver")
	}
	return info.Process, nil
}

type tunPipe struct {
	*os.File
	helper windows.Handle
	end    sync.Once
	err    error
}

func (s *tunPipe) Close() error {
	s.end.Do(func() {
		s.File.Close() // Pipe EOF asks the helper to remove its owned state.
		defer windows.CloseHandle(s.helper)
		event, err := windows.WaitForSingleObject(s.helper, 15000)
		var code uint32
		if err != nil || event != windows.WAIT_OBJECT_0 || windows.GetExitCodeProcess(s.helper, &code) != nil || code != 0 {
			s.err = tunFailure("tun_recovery")
		}
	})
	return s.err
}

func startTunHelper(mode string) (*tunPipe, error) {
	if mode != "full" && mode != "test" && mode != "recover" {
		return nil, tunFailure("tun_admin")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, tunFailure("tun_driver")
	}
	path := filepath.Join(filepath.Dir(exe), "nexus-tun-helper.exe")
	pin, err := tuntrust.Pin(path, tunHelperSHA256)
	if err != nil {
		return nil, tunFailure("tun_driver")
	}
	defer pin.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, tunFailure("tun_admin")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, tunFailure("tun_admin")
	}
	var random [32]byte
	rand.Read(random[:])
	nonce := hex.EncodeToString(random[:])
	name, _ := windows.UTF16PtrFromString(`\\.\pipe\NexusTun-` + nonce)
	attr := windows.SecurityAttributes{SecurityDescriptor: sd}
	attr.Length = uint32(unsafe.Sizeof(attr))
	h, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 0, &attr)
	if err != nil {
		return nil, tunFailure("tun_admin")
	}
	var process windows.Handle
	var file *os.File
	handedOff := false
	defer func() {
		if !handedOff {
			if file != nil {
				file.Close()
			} else {
				windows.CloseHandle(h)
			}
			if process != 0 {
				windows.CloseHandle(process)
			}
		}
	}()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, tunFailure("tun_admin")
	}
	defer windows.CloseHandle(event)
	ol := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(h, &ol)
	pending := errors.Is(err, windows.ERROR_IO_PENDING)
	if !pending && err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return nil, tunFailure("tun_admin")
	}
	defer func() {
		if pending {
			windows.CancelIoEx(h, &ol)
			var n uint32
			windows.GetOverlappedResult(h, &ol, &n, true)
		}
	}()
	service, err := requestTunService(path, mode, nonce)
	if err != nil {
		return nil, err
	}
	if service != nil {
		defer service.Close()
		process, err = servicePeer(service, path, h, event, pending)
	} else {
		process, err = launchTunHelper(path, mode, nonce)
	}
	if err != nil {
		return nil, err
	}
	if pending {
		signaled, err := windows.WaitForMultipleObjects([]windows.Handle{event, process}, false, 30000)
		if err != nil || signaled != windows.WAIT_OBJECT_0 {
			return nil, tunFailure("tun_admin")
		}
		var n uint32
		if windows.GetOverlappedResult(h, &ol, &n, false) != nil {
			return nil, tunFailure("tun_admin")
		}
		pending = false
	}
	pid, err := windows.GetProcessId(process)
	var peer uint32
	if err != nil || windows.GetNamedPipeClientProcessId(h, &peer) != nil || pid == 0 || peer != pid {
		return nil, tunFailure("tun_admin")
	}
	file = os.NewFile(uintptr(h), "Nexus TUN session")
	if service != nil {
		if err := file.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return nil, tunFailure("tun_admin")
		}
		if _, err := file.Write([]byte{tuntrust.ModeByte(mode)}); err != nil {
			return nil, tunFailure("tun_admin")
		}
		if err := file.SetWriteDeadline(time.Time{}); err != nil {
			return nil, tunFailure("tun_admin")
		}
	}
	if err := file.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return nil, tunFailure("tun_driver")
	}
	var status [1]byte
	if _, err := io.ReadFull(file, status[:]); err != nil {
		return nil, tunFailure("tun_driver")
	}
	switch status[0] {
	case 0:
	case 1:
		return nil, tunFailure("tun_admin")
	case 3:
		return nil, tunFailure("tun_conflict")
	case 4:
		return nil, tunFailure("tun_recovery")
	default:
		return nil, tunFailure("tun_driver")
	}
	file.SetReadDeadline(time.Time{})
	handedOff = true
	return &tunPipe{File: file, helper: process}, nil
}

var tunOwner struct {
	sync.Mutex
	pipe *tunPipe
}

func openTunSession(mode string) (io.ReadWriteCloser, error) {
	p, err := startTunHelper(mode)
	if err != nil {
		return nil, err
	}
	tunOwner.Lock()
	tunOwner.pipe = p
	tunOwner.Unlock()
	return p, nil
}

func closeTunOnStop() error {
	tunOwner.Lock()
	p := tunOwner.pipe
	tunOwner.Unlock()
	// Shutdown and stdin EOF can race. Both must wait for the same helper's rollback.
	if p != nil {
		return p.Close()
	}
	return nil
}

// A normal launch only reads the protected recovery record. Another account
// or a live helper keeps ownership; a recycled PID is identified by FILETIME.
func staleTunSession() (bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\NexusDesktopTun`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	data, _, err := k.GetBinaryValue("Session")
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var record struct {
		OwnerSID string
		PID      uint32
		Created  uint64
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &record) != nil || record.OwnerSID == "" || record.PID == 0 || record.Created == 0 {
		return false, tunFailure("tun_recovery")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || record.OwnerSID != user.User.Sid.String() {
		return false, err
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, record.PID)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(p)
	var created, exit, kernel, userTime windows.Filetime
	if err := windows.GetProcessTimes(p, &created, &exit, &kernel, &userTime); err != nil {
		return false, err
	}
	actual := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return actual != record.Created || exit.LowDateTime != 0 || exit.HighDateTime != 0, nil
}

func recoverTunCommand() string {
	stale, err := staleTunSession()
	if err != nil {
		return "tun_recovery"
	}
	if stale {
		p, err := startTunHelper("recover")
		if tunErrorCode(err) == "tun_admin" {
			return "tun_admin"
		}
		if err != nil || p.Close() != nil {
			return "tun_recovery"
		}
	}
	fmt.Println("recovered")
	return ""
}
