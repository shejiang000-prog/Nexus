package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xtls/libxray/enrollment"
	"golang.org/x/crypto/ssh"
)

const ownerLedger = "runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db"
const inviteMessage = "复制这条消息后打开 Nexus\n"

func ledgerExec(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	out, err := ownerExec(ctx, client, cmd, nil)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", ownerError("cancelled")
		case errors.Is(err, ownerError("ssh_timeout")), errors.Is(err, ownerError("ssh_refused")):
			return "", err
		default:
			return "", ownerError("ledger_failed")
		}
	}
	return strings.TrimSpace(out), nil
}

func ownerNow(ctx context.Context, client *ssh.Client) (time.Time, error) {
	s, err := ledgerExec(ctx, client, "date -u +%s")
	if err != nil {
		return time.Time{}, err
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > 253402300799-3650*86400 {
		return time.Time{}, ownerError("ledger_failed")
	}
	return time.Unix(n, 0).UTC(), nil
}

func ownerIssueLink(ctx context.Context, client *ssh.Client, account string, now time.Time, days *int) (string, int64, error) {
	if !validSourceID(account) {
		return "", 0, ownerError("ledger_failed")
	}
	valid := 7
	if days != nil {
		valid = min(valid, *days)
	}
	end := now.AddDate(0, 0, valid)
	out, err := ledgerExec(ctx, client, ownerLedger+" invite --link "+account+" "+end.Format(time.RFC3339))
	if err != nil {
		return "", 0, err
	}
	if _, err := enrollment.Parse(out); err != nil {
		return "", 0, ownerError("ledger_failed")
	}
	return out, end.Unix(), nil
}

type ownerDeviceView struct {
	DeviceID    string `json:"deviceId"`
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	ExpiresAt   *int64 `json:"expiresAt"`
	ExpiryKnown bool   `json:"expiryKnown"`
	Revoked     bool   `json:"revoked"`
}

type ledgerEntry struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Name      string          `json:"name"`
	Expiry    json.RawMessage `json:"expires_at"`
	RevokedAt *int64          `json:"revoked_at"`
}

func ledgerRows(ctx context.Context, client *ssh.Client, op string) ([]ledgerEntry, error) {
	out, err := ledgerExec(ctx, client, ownerLedger+" "+op)
	if err != nil {
		return nil, err
	}
	var rows []ledgerEntry
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r ledgerEntry
		if json.Unmarshal([]byte(line), &r) != nil || !validSourceID(r.ID) {
			return nil, ownerError("ledger_failed")
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func (r ledgerEntry) expiry() (*int64, error) {
	if len(r.Expiry) == 0 {
		return nil, nil
	}
	var n *int64
	if json.Unmarshal(r.Expiry, &n) != nil || n != nil && (*n < 0 || *n > 253402300799) {
		return nil, ownerError("ledger_failed")
	}
	return n, nil
}

func ownerDevices(ctx context.Context, client *ssh.Client) ([]ownerDeviceView, error) {
	rows, err := ledgerRows(ctx, client, "devices")
	if err != nil {
		return nil, err
	}
	devices := make([]ownerDeviceView, 0, len(rows))
	for _, r := range rows {
		if !validSourceID(r.UserID) {
			return nil, ownerError("ledger_failed")
		}
		n, err := r.expiry()
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(r.Name)
		// Only send human-readable device labels; hostile node output may contain
		// endpoints, links or control sequences.
		if name == "" || len(name) > 128 || strings.IndexFunc(name, func(c rune) bool {
			return !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == ' ' || c == '-' || c == '_')
		}) >= 0 {
			name = r.ID[:8]
		}
		devices = append(devices, ownerDeviceView{r.ID, r.UserID, name, n, r.Expiry != nil, r.RevokedAt != nil})
	}
	return devices, nil
}

func manageLedger(ctx context.Context, client *ssh.Client, req ownerRequest) (any, error) {
	switch req.Op {
	case "devices":
		return ownerDevices(ctx, client)
	case "revoke":
		if !validSourceID(req.DeviceID) {
			return nil, ownerError("invalid_request")
		}
		_, err := ledgerExec(ctx, client, ownerLedger+" revoke "+req.DeviceID)
		return nil, err
	case "renew":
		if !validSourceID(req.AccountID) {
			return nil, ownerError("invalid_request")
		}
		expiry := "never"
		if req.Days != nil {
			rows, err := ledgerRows(ctx, client, "users")
			if err != nil {
				return nil, err
			}
			var current *int64
			found := false
			for _, row := range rows {
				if row.ID != req.AccountID {
					continue
				}
				if row.Expiry == nil {
					return nil, ownerError("ledger_failed")
				}
				current, err = row.expiry()
				if err != nil {
					return nil, err
				}
				found = true
				break
			}
			if !found {
				return nil, ownerError("ledger_failed")
			}
			now, err := ownerNow(ctx, client)
			if err != nil {
				return nil, err
			}
			if current != nil && *current > now.Unix() {
				now = time.Unix(*current, 0).UTC()
			}
			end := now.AddDate(0, 0, *req.Days)
			if end.Year() > 9999 {
				return nil, ownerError("ledger_failed")
			}
			expiry = end.Format(time.RFC3339)
		}
		_, err := ledgerExec(ctx, client, ownerLedger+" renew "+req.AccountID+" "+expiry)
		return nil, err
	case "invite":
		now, err := ownerNow(ctx, client)
		if err != nil {
			return nil, err
		}
		cmd := ownerLedger + " create-user"
		if req.Days != nil {
			cmd += " " + now.AddDate(0, 0, *req.Days).Format(time.RFC3339)
		}
		account, err := ledgerExec(ctx, client, cmd)
		if err != nil {
			return nil, err
		}
		link, _, err := ownerIssueLink(ctx, client, account, now, req.Days)
		if err != nil {
			return nil, err
		}
		return map[string]string{"text": inviteMessage + link}, nil
	default:
		return nil, ownerError("invalid_request")
	}
}
