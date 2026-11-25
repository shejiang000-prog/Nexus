package main

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const journalPath = `SOFTWARE\NexusDesktopTun`

var busyNetwork = errors.New("another TUN session owns the adapter")
var staleNetwork = errors.New("TUN journal requires recovery")
var unsafeNetwork = errors.New("cannot establish safe capture on this network")
var routeConflict = errors.New("another network route competes with TUN capture")

func networkErrorCode(err error) string {
	switch {
	case errors.Is(err, busyNetwork):
		return "tun_busy"
	case errors.Is(err, staleNetwork):
		return "tun_recovery"
	case errors.Is(err, routeConflict):
		return "tun_conflict"
	case errors.Is(err, unsafeNetwork), errors.Is(err, noPhysicalDefault):
		return "tun_network"
	default:
		return "tun_setup"
	}
}

type netRecord struct {
	OwnerSID string   `json:"ownerSID"`
	PID      uint32   `json:"pid"`
	Created  uint64   `json:"created"`
	GUID     string   `json:"guid"`
	LUID     uint64   `json:"luid"`
	Mode     string   `json:"mode"`
	Changes  []string `json:"changes"`
}

func makeRecord(owner, mode string) (netRecord, error) {
	r := netRecord{OwnerSID: owner, Mode: mode, PID: uint32(os.Getpid())}
	if _, err := windows.StringToSid(owner); err != nil {
		return r, err
	}
	guid, err := windows.GenerateGUID()
	if err != nil {
		return r, err
	}
	r.GUID = guid.String()
	var a, b, c, d windows.Filetime
	err = windows.GetProcessTimes(windows.CurrentProcess(), &a, &b, &c, &d)
	r.Created = uint64(a.HighDateTime)<<32 | uint64(a.LowDateTime)
	return r, err
}

// ACL is supplied atomically on creation. An unexpected existing ACL is
// rejected before treating registry bytes as privileged recovery data.
func openJournal() (registry.Key, error) {
	sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;;KA;;;SY)(A;;KA;;;BA)(A;;KR;;;BU)")
	if err != nil {
		return 0, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	var key registry.Key
	var disp uint32
	path, _ := windows.UTF16PtrFromString(journalPath)
	rc, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegCreateKeyExW").Call(uintptr(registry.LOCAL_MACHINE), uintptr(unsafe.Pointer(path)), 0, 0, 0, registry.ALL_ACCESS|registry.WOW64_64KEY, uintptr(unsafe.Pointer(&sa)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&disp)))
	if rc != 0 {
		return 0, syscall.Errno(rc)
	}
	err = checkJournalACL(key)
	if err != nil {
		key.Close()
		return 0, err
	}
	return key, nil
}
func privilegedSID(s *windows.SID) bool {
	return s != nil && (s.IsWellKnown(windows.WinBuiltinAdministratorsSid) || s.IsWellKnown(windows.WinLocalSystemSid))
}
func checkJournalACL(key registry.Key) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(key), windows.SE_REGISTRY_KEY, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	own, _, err := sd.Owner()
	if err != nil || !privilegedSID(own) {
		return staleNetwork
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return staleNetwork
	}
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return staleNetwork
		}
		if !privilegedSID((*windows.SID)(unsafe.Pointer(&ace.SidStart))) && uint32(ace.Mask)&^uint32(registry.READ) != 0 {
			return staleNetwork
		}
	}
	return nil
}
func lockNetwork() (func(), error) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;BA)(A;;GA;;;SY)")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, _ := windows.UTF16PtrFromString(`Global\NexusDesktopTun.v1`)
	m, err := windows.CreateMutexEx(&sa, name, 0, windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	runtime.LockOSThread()
	n, e := windows.WaitForSingleObject(m, 0)
	if e != nil || n != windows.WAIT_OBJECT_0 && n != windows.WAIT_ABANDONED {
		windows.CloseHandle(m)
		runtime.UnlockOSThread()
		return nil, busyNetwork
	}
	return func() { windows.ReleaseMutex(m); windows.CloseHandle(m); runtime.UnlockOSThread() }, nil
}
func loadJournal(k registry.Key) (*netRecord, error) {
	var r netRecord
	b, _, err := k.GetBinaryValue("Session")
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 32768 || json.Unmarshal(b, &r) != nil || r.OwnerSID == "" || r.PID == 0 || r.Created == 0 || r.Mode != "test" && r.Mode != "full" {
		return nil, staleNetwork
	}
	g, err := windows.GUIDFromString(r.GUID)
	if err != nil || g.String() != r.GUID {
		return nil, staleNetwork
	}
	return &r, nil
}
func flushJournal(k registry.Key) error {
	rc, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegFlushKey").Call(uintptr(k))
	if rc != 0 {
		return syscall.Errno(rc)
	}
	return nil
}
func commitJournal(k registry.Key, r *netRecord) error {
	if r == nil {
		if err := k.DeleteValue("Session"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	} else {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if err = k.SetBinaryValue("Session", b); err != nil {
			return err
		}
	}
	return flushJournal(k)
}
