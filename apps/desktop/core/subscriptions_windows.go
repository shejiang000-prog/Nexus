package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unsafe"

	"github.com/xtls/libxray/subscriptions"
	"golang.org/x/sys/windows"
)

type savedSource struct {
	URL, Name, Revision string
	Nodes               []subscriptions.Node
	allowance
}

type nodeChoice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type sourceView struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Refreshable bool         `json:"refreshable"`
	Nodes       []nodeChoice `json:"nodes"`
	allowance
}

func subscriptionError(err error) string {
	switch {
	case errors.Is(err, errSubFormat):
		return "subscription_format"
	case errors.Is(err, errSubDownload):
		return "subscription_download"
	case errors.Is(err, errSubChanged):
		return "subscription_changed"
	default:
		return "subscription_storage"
	}
}

func newSourceID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validSourceID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

// Selection survives reordering, but never silently connects to a replacement
// with different credentials or an endpoint reusing the same display name.
func choiceID(source string, n subscriptions.Node) string {
	h := sha256.New()
	io.WriteString(h, source+"\x00"+n.Name+"\x00"+n.Protocol+"\x00")
	h.Write(n.Outbound)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func publicName(name, fallback string) string {
	if name == "" || len(name) > 512 || strings.Contains(name, "://") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fallback
	}
	return name
}

// The lock covers only local snapshots and atomic writes, never a download.
func withSourceStore(fn func(dir string) error) error {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return errors.New("missing profile")
	}
	dir := filepath.Join(base, "Nexus", "desktop", "subscriptions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{}); err != nil {
		return err
	}
	return fn(dir)
}

// DPAPI defaults to the current user. The OS buffer must be freed using LocalFree.
func sourceDPAPI(data []byte, protect bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 32<<20 {
		return nil, errors.New("invalid source record size")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	buf := unsafe.Slice(out.Data, out.Size)
	defer clear(buf)
	return bytes.Clone(buf), nil
}

func loadSource(dir, id string) (*savedSource, error) {
	if !validSourceID(id) {
		return nil, errSubChanged
	}
	f, err := os.Open(filepath.Join(dir, id+".dpapi"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errSubChanged
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	encrypted, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	plain, err := sourceDPAPI(encrypted, false)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var s savedSource
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func sourceIDs(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		if id, ok := strings.CutSuffix(f.Name(), ".dpapi"); ok && validSourceID(id) && !f.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func catalog(dir string) ([]sourceView, error) {
	ids, err := sourceIDs(dir)
	if err != nil {
		return nil, err
	}
	views := make([]sourceView, 0, len(ids))
	for _, id := range ids {
		s, err := loadSource(dir, id)
		if err != nil {
			// Keep other sources usable and let the owner remove the unreadable one.
			views = append(views, sourceView{ID: id, Name: "本机订阅无法读取", Nodes: []nodeChoice{}})
			continue
		}
		view := sourceView{ID: id, Name: s.Name, Refreshable: s.URL != "", allowance: s.allowance}
		for _, n := range s.Nodes {
			view.Nodes = append(view.Nodes, nodeChoice{ID: choiceID(id, n), Name: publicName(n.Name, n.Protocol), Protocol: n.Protocol})
		}
		views = append(views, view)
	}
	return views, nil
}

func subscriptionNode(sourceID, nodeID string) (subscriptions.Node, error) {
	var node subscriptions.Node
	err := withSourceStore(func(dir string) error {
		s, err := loadSource(dir, sourceID)
		if err != nil {
			return err
		}
		for _, n := range s.Nodes {
			if choiceID(sourceID, n) == nodeID {
				node = n
				return nil
			}
		}
		return errSubChanged
	})
	return node, err
}

func importSource(input string) (*savedSource, error) {
	if len(input) > subscriptions.MaxBytes {
		return nil, errSubFormat
	}
	input = strings.TrimSpace(input)
	body := []byte(input)
	source := &savedSource{Name: "分享链接", Revision: newSourceID()}
	var usage string
	if !strings.ContainsAny(input, "\r\n") && (strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "http://")) {
		u, err := url.Parse(input)
		if err != nil {
			return nil, errSubFormat
		}
		body, usage, err = fetchSubscription(u)
		if err != nil {
			return nil, err
		}
		source.URL, source.Name = input, publicName(u.Hostname(), "订阅")
	}
	nodes, err := subscriptions.Parse(body)
	if err != nil {
		return nil, errSubFormat
	}
	for _, n := range nodes {
		if !source.infoName(n.Name) {
			source.Nodes = append(source.Nodes, n)
		}
	}
	if len(source.Nodes) == 0 {
		return nil, errSubFormat
	}
	if source.URL == "" && len(source.Nodes) == 1 {
		source.Name = publicName(source.Nodes[0].Name, source.Name)
	}
	source.applyHeader(usage)
	return source, nil
}

func manageSubscriptions(action, input, id string) ([]sourceView, error) {
	var replacement *savedSource
	var revision string
	var err error
	switch action {
	case "update_subscription":
		err = withSourceStore(func(dir string) error {
			s, err := loadSource(dir, id)
			if err != nil {
				return err
			}
			if s.URL == "" {
				return errSubFormat
			}
			input, revision = s.URL, s.Revision
			return nil
		})
		if err != nil {
			return nil, err
		}
		fallthrough
	case "import_subscription":
		replacement, err = importSource(input)
		if err != nil {
			return nil, err
		}
	case "subscriptions", "remove_subscription":
	default:
		return nil, errSubFormat
	}
	var views []sourceView
	err = withSourceStore(func(dir string) error {
		switch action {
		case "import_subscription":
			ids, err := sourceIDs(dir)
			if err != nil {
				return err
			}
			if len(ids) >= 32 {
				return errSubFormat
			}
			id = newSourceID()
		case "update_subscription":
			s, err := loadSource(dir, id)
			if err != nil {
				return err
			}
			if s.Revision != revision {
				return errSubChanged
			}
		case "remove_subscription":
			if !validSourceID(id) {
				return errSubChanged
			}
			if err := os.Remove(filepath.Join(dir, id+".dpapi")); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return errSubChanged
				}
				return err
			}
		}
		if replacement != nil {
			plain, err := json.Marshal(replacement)
			if err != nil {
				return err
			}
			defer clear(plain)
			encrypted, err := sourceDPAPI(plain, true)
			if err != nil {
				return err
			}
			if err := save(filepath.Join(dir, id+".dpapi"), encrypted); err != nil {
				return err
			}
		}
		views, err = catalog(dir)
		return err
	})
	return views, err
}

func subscriptionCommand(action, input, id string) string {
	views, err := manageSubscriptions(action, input, id)
	if err != nil {
		return subscriptionError(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string][]sourceView{"subscriptions": views}); err != nil {
		return "subscription_storage"
	}
	return ""
}
