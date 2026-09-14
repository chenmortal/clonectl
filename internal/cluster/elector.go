// Package cluster implements active-standby leader election via a DB row +
// lease renewal (port of app/cluster/election.py). Two nodes share one MySQL;
// each runs HeartbeatTick and ElectTick (driven by the shared gocron
// scheduler — this package owns no timers). Concurrency safety comes from
// InnoDB row locks on the single leader_lease row plus WHERE-clause
// re-evaluation inside the CAS UPDATE; the same logic works against SQLite.
package cluster

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"

	"rclone_sync/internal/database"
)

// LeaderElector is the per-process election participant.
type LeaderElector struct {
	db                *gorm.DB
	NodeID            string
	ClusterName       string
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	OnAcquired        func() // called on leadership gain; must not panic
	OnLost            func() // called on leadership loss; must not panic

	mu       sync.Mutex
	isLeader bool
	leaderID *string
}

// New builds an elector. Empty nodeID falls back to "<hostname>-<pid>".
func New(db *gorm.DB, nodeID, clusterName string, heartbeat, lease time.Duration) *LeaderElector {
	if nodeID == "" {
		host, _ := os.Hostname()
		nodeID = host + "-" + itoa(os.Getpid())
	}
	if clusterName == "" {
		clusterName = "default"
	}
	return &LeaderElector{
		db:                db,
		NodeID:            nodeID,
		ClusterName:       clusterName,
		HeartbeatInterval: heartbeat,
		LeaseDuration:     lease,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// IsLeader reports current leadership.
func (e *LeaderElector) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isLeader
}

// GetNodeID returns this node's identity (method form for the api.ElectorInfo
// interface; the NodeID field stays the canonical storage).
func (e *LeaderElector) GetNodeID() string { return e.NodeID }

// Identity returns (leaderID, clusterName) for the 503 gate body.
func (e *LeaderElector) Identity() (leaderID, clusterName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.leaderID != nil {
		return *e.leaderID, e.ClusterName
	}
	return "", e.ClusterName
}

// Join registers (idempotently) this node and ensures the lease row exists.
// Called before the scheduler starts ticking.
func (e *LeaderElector) Join() error {
	now := database.NowUTC()
	return e.db.Transaction(func(tx *gorm.DB) error {
		var lease database.LeaderLease
		if err := tx.First(&lease, "cluster_name = ?", e.ClusterName).Error; err != nil {
			if err := tx.Create(&database.LeaderLease{ClusterName: e.ClusterName}).Error; err != nil {
				return err
			}
		}
		// Idempotent (re-)join: drop any stale row for this node, then insert.
		if err := tx.Exec("DELETE FROM cluster_nodes WHERE node_id = ?", e.NodeID).Error; err != nil {
			return err
		}
		host, _ := os.Hostname()
		return tx.Create(&database.ClusterNode{
			NodeID:        e.NodeID,
			ClusterName:   e.ClusterName,
			Role:          "standby",
			Hostname:      &host,
			StartedAt:     now,
			LastHeartbeat: now,
		}).Error
	})
}

// HeartbeatTick refreshes liveness; if leader, extends the lease. A failed
// extension (rowcount == 0) means we lost the lease — drop leadership.
func (e *LeaderElector) HeartbeatTick() {
	defer e.recoverPanic("heartbeat tick")
	now := database.NowUTC()
	err := e.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"UPDATE cluster_nodes SET last_heartbeat = ? WHERE node_id = ?",
			now, e.NodeID,
		).Error; err != nil {
			return err
		}
		if e.IsLeader() {
			leaseUntil := now.Add(e.LeaseDuration)
			res := tx.Exec(
				"UPDATE leader_lease SET lease_until = ? WHERE cluster_name = ? AND leader_node_id = ?",
				leaseUntil, e.ClusterName, e.NodeID,
			)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				slog.Warn("lease heartbeat failed (no longer owner); releasing leadership", "node", e.NodeID)
				e.setLeaderState(false)
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("LeaderElector heartbeat failed", "err", err)
	}
}

// ElectTick claims leadership when the lease is unowned or expired.
func (e *LeaderElector) ElectTick() {
	defer e.recoverPanic("elect tick")
	err := e.db.Transaction(func(tx *gorm.DB) error {
		var lease database.LeaderLease
		if err := tx.First(&lease, "cluster_name = ?", e.ClusterName).Error; err != nil {
			// Row vanished (manual ops); recreate and retry next tick.
			return tx.Create(&database.LeaderLease{ClusterName: e.ClusterName}).Error
		}

		now := database.NowUTC()
		expired := lease.LeaderNodeID == nil || lease.LeaseUntil == nil || !lease.LeaseUntil.After(now)
		if !expired {
			e.mu.Lock()
			e.leaderID = lease.LeaderNodeID
			if e.isLeader && (lease.LeaderNodeID == nil || *lease.LeaderNodeID != e.NodeID) {
				e.mu.Unlock()
				e.setLeaderState(false)
				return nil
			}
			e.mu.Unlock()
			return nil
		}

		leaseUntil := now.Add(e.LeaseDuration)
		res := tx.Exec(
			"UPDATE leader_lease SET leader_node_id = ?, lease_until = ?, epoch = epoch + 1 "+
				"WHERE cluster_name = ? AND (leader_node_id IS NULL OR lease_until <= ?)",
			e.NodeID, leaseUntil, e.ClusterName, now,
		)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			if err := tx.Exec(
				"UPDATE cluster_nodes SET role = 'leader' WHERE node_id = ?", e.NodeID,
			).Error; err != nil {
				return err
			}
			slog.Info("acquired leadership", "node", e.NodeID, "epoch", lease.Epoch+1)
			e.setLeaderState(true)
		}
		return nil
	})
	if err != nil {
		slog.Error("LeaderElector elect tick failed", "err", err)
	}
}

// Stop releases the lease if held and marks this node as left.
func (e *LeaderElector) Stop() {
	now := database.NowUTC()
	_ = e.db.Transaction(func(tx *gorm.DB) error {
		if e.IsLeader() {
			tx.Exec(
				"UPDATE leader_lease SET leader_node_id = NULL, lease_until = NULL "+
					"WHERE cluster_name = ? AND leader_node_id = ?",
				e.ClusterName, e.NodeID,
			)
			e.mu.Lock()
			e.isLeader = false
			e.leaderID = nil
			e.mu.Unlock()
		}
		return tx.Exec(
			"UPDATE cluster_nodes SET role = 'left', left_at = ? WHERE node_id = ?",
			now, e.NodeID,
		).Error
	})
	slog.Info("LeaderElector stopped", "node", e.NodeID)
}

// Peers lists live peer node_ids (heartbeat within the lease window, minus self).
func (e *LeaderElector) Peers() []string {
	cutoff := database.NowUTC().Add(-e.heartbeatWindow())
	var rows []database.ClusterNode
	if err := e.db.Where(
		"cluster_name = ? AND left_at IS NULL AND last_heartbeat >= ?",
		e.ClusterName, cutoff,
	).Find(&rows).Error; err != nil {
		slog.Error("LeaderElector peer list failed", "err", err)
		return []string{}
	}
	peers := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.NodeID != e.NodeID {
			peers = append(peers, r.NodeID)
		}
	}
	return peers
}

func (e *LeaderElector) heartbeatWindow() time.Duration {
	w := 2 * e.HeartbeatInterval
	if e.LeaseDuration > w {
		w = e.LeaseDuration
	}
	return w
}

// setLeaderState dedups transitions and fires callbacks (panic-guarded).
func (e *LeaderElector) setLeaderState(value bool) {
	e.mu.Lock()
	if value == e.isLeader {
		e.mu.Unlock()
		return
	}
	e.isLeader = value
	if value {
		id := e.NodeID
		e.leaderID = &id
	} else {
		e.leaderID = nil
	}
	e.mu.Unlock()

	if value && e.OnAcquired != nil {
		e.safeCall(e.OnAcquired, "on_acquired")
	}
	if !value && e.OnLost != nil {
		e.safeCall(e.OnLost, "on_lost")
	}
}

func (e *LeaderElector) safeCall(f func(), name string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("LeaderElector callback panicked", "callback", name, "panic", r)
		}
	}()
	f()
}

func (e *LeaderElector) recoverPanic(where string) {
	if r := recover(); r != nil {
		slog.Error("LeaderElector tick panicked", "where", where, "panic", r)
	}
}
