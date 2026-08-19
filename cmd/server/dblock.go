package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// serveStoreLockKey is the PostgreSQL advisory-lock key that guards the
// datastore against a restore running while a server is live.
//
// The number is an 8-byte ASCII tag, "AZMSERVE", packed big-endian into an
// int64 — the same self-describing scheme internal/testutil's buildLockKey uses
// for "AZMTMPL\0", so a DBA who finds it in pg_locks can read what it is. It is
// deliberately distinct from that template-build key: the two locks guard
// unrelated things and must never collide.
//
// This single symbol is the whole coordination mechanism. serve takes it
// (serveAcquireStoreLock) and restore probes it (restoreTryStoreLock); because
// both sites name THIS constant, "the two sides use the same key" is guaranteed
// at compile time rather than asserted at runtime.
const serveStoreLockKey int64 = 0x415A_4D53_4552_5645 // "AZMSERVE"

// storeLockConnectTimeout bounds establishing the dedicated lock connection and
// running the one lock statement on it. The advisory-lock probe itself is
// effectively instantaneous — pg_try_advisory_lock never waits, and serve's
// shared acquisition only waits if a restore is actively holding the exclusive
// lock — so this really only guards the TCP+auth handshake, so that an
// unreachable database surfaces as an error instead of a hang.
const storeLockConnectTimeout = 10 * time.Second

// errServerRunning is restore's refusal when a server holds the datastore lock.
//
// The message IS the operator instruction, verbatim: a restore aborts here at
// the one moment the operator most needs to be told what to do, so the sentence
// says it rather than describing the lock.
var errServerRunning = errors.New("the server is running; stop it before restoring")

// The two sides take the SAME key (serveStoreLockKey) in DIFFERENT modes, and
// the asymmetry is the design, not an oversight:
//
//   - serve takes it in SHARED mode. Shared locks do not conflict with one
//     another, so any number of server instances — a rolling deploy, an
//     HA pair — can hold it at once, and serve never blocks on another serve.
//     It holds the lock for the process's whole lifetime.
//   - restore takes it in EXCLUSIVE mode, non-blocking. An exclusive request
//     conflicts with a shared holder, so it FAILS the instant any server is
//     live — which is exactly the invariant "no restore while a server runs".
//     Two concurrent restores exclude each other for free, for the same reason.
//
// pg_advisory_lock*/pg_try_advisory_lock* are SESSION-scoped: the lock lives on
// the connection that took it and is released the moment that connection
// closes. So each side holds a dedicated connection open for exactly as long as
// the lock must be held, and an operator who kills either process can never
// strand a lock behind them.

// storeLock is a held advisory lock plus the dedicated connection whose session
// owns it. Closing that connection is what releases the lock, so Release needs
// to do nothing more.
type storeLock struct {
	conn *pgx.Conn
}

// Release drops the lock by closing its connection. Safe to call once; a second
// call is a no-op. Uses a background context because it must run during
// shutdown, when the request/boot context may already be cancelled.
func (l *storeLock) Release() {
	if l == nil || l.conn == nil {
		return
	}
	_ = l.conn.Close(context.Background())
	l.conn = nil
}

// serveAcquireStoreLock opens a dedicated connection and takes the datastore
// lock in shared mode, to be held for the server's whole lifetime.
//
// It does NOT draw the connection from the request-serving pool: the lock must
// outlive any single pooled connection, and permanently checking one out of the
// pool would just be a worse-behaved version of a dedicated connection.
func serveAcquireStoreLock(ctx context.Context, databaseURL string) (*storeLock, error) {
	lockCtx, cancel := context.WithTimeout(ctx, storeLockConnectTimeout)
	defer cancel()

	conn, err := pgx.Connect(lockCtx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting for datastore lock: %w", err)
	}
	if _, err := conn.Exec(lockCtx, "SELECT pg_advisory_lock_shared($1)", serveStoreLockKey); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("taking datastore lock: %w", err)
	}
	return &storeLock{conn: conn}, nil
}

// restoreTryStoreLock takes the datastore lock in exclusive mode without
// blocking, holding it for the caller to Release when the restore finishes (so
// a server cannot start underneath a restore in progress either). It returns:
//
//   - (lock, nil)             the lock was free; the caller owns it and must Release
//   - (nil, errServerRunning) a server (or another restore) holds it — refuse
//   - (nil, err)              the attempt itself could not be made
func restoreTryStoreLock(ctx context.Context, databaseURL string) (*storeLock, error) {
	lockCtx, cancel := context.WithTimeout(ctx, storeLockConnectTimeout)
	defer cancel()

	conn, err := pgx.Connect(lockCtx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting for datastore lock: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(lockCtx, "SELECT pg_try_advisory_lock($1)", serveStoreLockKey).Scan(&acquired); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("checking datastore lock: %w", err)
	}
	if !acquired {
		_ = conn.Close(context.Background())
		return nil, errServerRunning
	}
	return &storeLock{conn: conn}, nil
}
