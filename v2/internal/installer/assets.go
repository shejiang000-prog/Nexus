package installer

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type asset struct{ name, url, sha, binary string }

func release(p, arch string) asset {
	if p == "reality" {
		name, hash := "Xray-linux-64.zip", "b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82"
		if arch == "arm64" {
			name, hash = "Xray-linux-arm64-v8a.zip", "64ed6858982a7f5eb85d88b811089063bd4f17561f4c8f44e0f1dc8e6f6430be"
		}
		return asset{name, "https://github.com/XTLS/Xray-core/releases/download/v26.11.9/" + name, hash, "xray"}
	}
	name := "hysteria-linux-" + arch
	hash := "fc25edff9948708cb00a4ff01f3bccde677660aca050133bb1ad46c6860362d2"
	if arch == "arm64" {
		hash = "e7c52241299fe85aba11e9d36e85d8d88ddbb5799ac0e33d272219d20e313bab"
	}
	return asset{name, "https://github.com/apernet/hysteria/releases/download/app%2Fv2.14.0/" + name, hash, "hysteria"}
}

const assetLimit = 256 << 20

func copyFile(dst string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, assetLimit+1))
	if n > assetLimit {
		err = errors.New("asset exceeds size limit")
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		os.Remove(dst)
	}
	return err
}
func copyPath(dst, src string, mode os.FileMode) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return copyFile(dst, f, mode)
}
func verifyELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("invalid Linux executable %s", filepath.Base(path))
	}
	defer f.Close()
	want := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		want = elf.EM_AARCH64
	}
	if f.Machine != want || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("wrong executable architecture: %s", filepath.Base(path))
	}
	return nil
}
func fetch(ctx context.Context, a asset, cache, dir string) error {
	var body io.ReadCloser
	var err error
	if cache != "" {
		body, err = os.Open(filepath.Join(cache, a.name))
	} else {
		req, e := http.NewRequestWithContext(ctx, "GET", a.url, nil)
		if e != nil {
			return e
		}
		client := http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := strings.ToLower(req.URL.Hostname())
			if len(via) > 8 || req.URL.Scheme != "https" || host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com") {
				return errors.New("untrusted release redirect")
			}
			return nil
		}}
		resp, e := client.Do(req)
		if e != nil {
			return fmt.Errorf("download %s failed", a.name)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("download %s: HTTP %d", a.name, resp.StatusCode)
		}
		body = resp.Body
	}
	if err != nil {
		return err
	}
	defer body.Close()
	download := filepath.Join(dir, a.name)
	hash := sha256.New()
	if err := copyFile(download, io.TeeReader(body, hash), 0600); err != nil {
		return err
	}
	defer os.Remove(download)
	if hex.EncodeToString(hash.Sum(nil)) != a.sha {
		return fmt.Errorf("SHA-256 mismatch for %s", a.name)
	}
	dst := filepath.Join(dir, a.binary)
	if a.binary == "xray" {
		z, e := zip.OpenReader(download)
		if e != nil {
			return e
		}
		defer z.Close()
		var payload *zip.File
		for _, f := range z.File {
			if f.Name == "xray" {
				if payload != nil || !f.Mode().IsRegular() {
					return errors.New("invalid Xray archive executable")
				}
				payload = f
			}
		}
		if payload == nil {
			return errors.New("Xray archive lacks executable")
		}
		rc, e := payload.Open()
		if e != nil {
			return e
		}
		defer rc.Close()
		err = copyFile(dst, rc, 0755)
	} else {
		err = os.Rename(download, dst)
		if err == nil {
			err = os.Chmod(dst, 0755)
		}
	}
	if err != nil {
		return err
	}
	return verifyELF(dst)
}
func stageBinaries(ctx context.Context, o Options, dir string) error {
	src := o.NodePath
	if src == "" {
		var e error
		src, e = os.Executable()
		if e != nil {
			return e
		}
	}
	dst := filepath.Join(dir, "nexus-node")
	if err := copyPath(dst, src, 0755); err != nil {
		return err
	}
	if err := verifyELF(dst); err != nil {
		return err
	}
	for _, p := range protocols {
		if _, ok := o.Ports[p]; ok && p != "v2" {
			if err := fetch(ctx, release(p, runtime.GOARCH), o.AssetDir, dir); err != nil {
				return err
			}
		}
	}
	return nil
}
