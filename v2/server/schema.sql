CREATE TABLE users (
    id BLOB PRIMARY KEY NOT NULL CHECK(length(id)=16),
    quota_bytes INTEGER CHECK(quota_bytes>=0),
    expires_at INTEGER
);
CREATE TABLE invites (
    digest BLOB PRIMARY KEY NOT NULL CHECK(length(digest)=32),
    user_id BLOB NOT NULL REFERENCES users(id),
    expires_at INTEGER NOT NULL,
    remaining INTEGER NOT NULL CHECK(remaining BETWEEN 0 AND 1)
);
CREATE TABLE devices (
    id BLOB PRIMARY KEY NOT NULL CHECK(length(id)=16),
    invite_digest BLOB NOT NULL REFERENCES invites(digest),
    request_id BLOB NOT NULL UNIQUE CHECK(length(request_id)=16),
    public_key BLOB NOT NULL UNIQUE CHECK(length(public_key)=33),
    name TEXT NOT NULL,
    revoked_at INTEGER
);
CREATE TABLE audit (
    seq INTEGER PRIMARY KEY,
    occurred_at INTEGER NOT NULL,
    action TEXT NOT NULL,
    device_id BLOB REFERENCES devices(id),
    user_id BLOB REFERENCES users(id)
);
PRAGMA user_version=2;
