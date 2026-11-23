package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const inboundTag = "nexus-reality"

func inboundClients(clients map[string]string) []object {
	out := make([]object, 0, len(clients))
	for id, uuid := range clients {
		out = append(out, object{"email": id, "id": uuid, "flow": "xtls-rprx-vision"})
	}
	return out
}

func (s *service) inbound(clients map[string]string) object {
	return object{"tag": inboundTag, "port": s.site.Ports["reality"], "protocol": "vless", "settings": object{"decryption": "none", "clients": inboundClients(clients)}}
}

func (s *service) startXray(clients map[string]string, key RealityKey) (*worker, error) {
	w, err := s.start("xray", func(dir string) object {
		listener := s.inbound(clients)
		listener["streamSettings"] = object{"security": "reality", "network": "raw", "realitySettings": object{
			"target": s.site.Target, "serverNames": s.site.ServerNames, "privateKey": key.PrivateKey, "shortIds": []string{key.ShortID}}}
		return object{"log": object{"loglevel": "none"},
			"api":      object{"tag": "api", "listen": filepath.Join(dir, "api.sock"), "services": []string{"HandlerService"}},
			"inbounds": []object{listener}, "outbounds": []object{{"protocol": "freedom", "settings": object{"targetStrategy": "ForceIP", "finalRules": []object{{"action": "block", "ip": denied}}}}}}
	}, "run", "-config")
	if err != nil {
		return nil, err
	}
	w.address = filepath.Join(w.dir, "api.sock")
	err = w.ready(func() bool {
		if _, err := os.Stat(w.address); err != nil {
			return false
		}
		actual, err := s.readXrayUsers(w)
		return err == nil && maps.Equal(clients, actual)
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *service) xrayCommand(w *worker, input []byte, command string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	args := []string{"api", command, "-server=unix://" + w.address, "-timeout=3"}
	cmd := exec.CommandContext(ctx, filepath.Join(s.root, "opt/nexus-v2/bin/xray"), append(args, arguments...)...)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Xray user management unavailable")
	}
	return out, nil
}

func (s *service) readXrayUsers(w *worker) (map[string]string, error) {
	data, err := s.xrayCommand(w, nil, "inbounduser", "-tag="+inboundTag)
	if err != nil {
		return nil, err
	}
	var result struct {
		Users []struct {
			Email   string
			Account struct{ ID string }
		}
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, errors.New("invalid Xray user list")
	}
	users := make(map[string]string, len(result.Users))
	for _, u := range result.Users {
		users[u.Email] = u.Account.ID
	}
	return users, nil
}

func (s *service) updateXrayUsers(w *worker, desired map[string]string) error {
	actual, err := s.readXrayUsers(w)
	if err != nil {
		return err
	}
	if maps.Equal(actual, desired) {
		return nil
	}
	var remove []string
	for id, uuid := range actual {
		if desired[id] != uuid {
			remove = append(remove, id)
		}
	}
	// Bound argv size; UUIDs only travel over stdin and the private management socket.
	for len(remove) > 0 {
		count := min(100, len(remove))
		args := append([]string{"-tag=" + inboundTag}, remove[:count]...)
		if _, err = s.xrayCommand(w, nil, "rmu", args...); err != nil {
			return err
		}
		remove = remove[count:]
	}
	add := map[string]string{}
	for id, uuid := range desired {
		if actual[id] != uuid {
			add[id] = uuid
		}
	}
	if len(add) > 0 {
		data, err := json.Marshal(object{"inbounds": []object{s.inbound(add)}})
		if err != nil {
			return err
		}
		if _, err = s.xrayCommand(w, data, "adu", "stdin:"); err != nil {
			return err
		}
	}
	// The pinned CLI can exit successfully after individual add/remove failures.
	actual, err = s.readXrayUsers(w)
	if err != nil {
		return err
	}
	if !maps.Equal(actual, desired) {
		return errors.New("Xray did not apply the requested users")
	}
	return nil
}

func (s *service) reconcileXray() error {
	desired := map[string]string{}
	var expires int64
	now := time.Now().Unix()
	for id, c := range s.users {
		if c.ExpiresAt > 0 && c.ExpiresAt <= now {
			continue
		}
		desired[id] = c.UUID
		if c.ExpiresAt > 0 && (expires == 0 || c.ExpiresAt < expires) {
			expires = c.ExpiresAt
		}
	}
	key := *s.live.RealityKey
	if s.xray != nil && (!s.xray.running() || s.xrayKey != key) {
		s.xray.stop()
		s.xray = nil
	}
	var err error
	if s.xray == nil {
		s.xray, err = s.startXray(desired, key)
	} else if err = s.updateXrayUsers(s.xray, desired); err != nil {
		// A partial upstream mutation must not leave revoked credentials admitted.
		s.xray.stop()
		s.xray = nil
	}
	if err != nil {
		return err
	}
	s.xrayKey = key
	s.nextExpiry = expires
	return nil
}
