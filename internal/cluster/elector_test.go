package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rclone_sync/internal/database"
)

func newElectorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func TestJoinCreatesNodeAndLeaseRows(t *testing.T) {
	db := newElectorDB(t)
	e := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, e.Join())

	var lease database.LeaderLease
	require.NoError(t, db.First(&lease, "cluster_name = ?", "c1").Error)
	assert.Nil(t, lease.LeaderNodeID)

	var node database.ClusterNode
	require.NoError(t, db.First(&node, "node_id = ?", "nodeA").Error)
	assert.Equal(t, "standby", node.Role)

	// Re-join is idempotent.
	require.NoError(t, e.Join())
	var count int64
	db.Model(&database.ClusterNode{}).Count(&count)
	assert.EqualValues(t, 1, count)
}

func TestElectAcquireUnownedLease(t *testing.T) {
	db := newElectorDB(t)
	e := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, e.Join())

	acquired := false
	e.OnAcquired = func() { acquired = true }
	e.ElectTick()

	assert.True(t, e.IsLeader())
	assert.True(t, acquired)
	leaderID, _ := e.Identity()
	assert.Equal(t, "nodeA", leaderID)

	var lease database.LeaderLease
	require.NoError(t, db.First(&lease, "cluster_name = ?", "c1").Error)
	require.NotNil(t, lease.LeaderNodeID)
	assert.Equal(t, "nodeA", *lease.LeaderNodeID)
	assert.EqualValues(t, 1, lease.Epoch)

	var node database.ClusterNode
	require.NoError(t, db.First(&node, "node_id = ?", "nodeA").Error)
	assert.Equal(t, "leader", node.Role)
}

func TestElectBlockedByValidLease(t *testing.T) {
	db := newElectorDB(t)
	a := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, a.Join())
	a.ElectTick()
	require.True(t, a.IsLeader())

	b := New(db, "nodeB", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, b.Join())
	b.ElectTick()

	assert.False(t, b.IsLeader())
	leaderID, _ := b.Identity()
	assert.Equal(t, "nodeA", leaderID, "standby records who holds the lease")

	var lease database.LeaderLease
	require.NoError(t, db.First(&lease, "cluster_name = ?", "c1").Error)
	assert.EqualValues(t, 1, lease.Epoch, "no second CAS win")
}

func TestElectTakesOverExpiredLease(t *testing.T) {
	db := newElectorDB(t)
	a := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, a.Join())
	a.ElectTick()
	require.True(t, a.IsLeader())

	// Expire the lease without A releasing (simulates crash).
	past := database.NowUTC().Add(-time.Second)
	require.NoError(t, db.Exec(
		"UPDATE leader_lease SET lease_until = ? WHERE cluster_name = ?", past, "c1").Error)

	b := New(db, "nodeB", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, b.Join())
	lostA := false
	a.OnLost = func() { lostA = true }
	b.ElectTick()

	assert.True(t, b.IsLeader())

	// A only learns it lost when its OWN tick observes the stolen lease
	// (heartbeat extension rowcount==0, or elect seeing another holder) —
	// Python semantics.
	a.ElectTick()
	assert.False(t, a.IsLeader(), "A drops leadership on its next observation")
	assert.True(t, lostA, "A's OnLost fired via elect observation")
}

func TestHeartbeatExtendsLeaseAndDetectsLoss(t *testing.T) {
	db := newElectorDB(t)
	e := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, e.Join())
	e.ElectTick()
	require.True(t, e.IsLeader())

	var before database.LeaderLease
	require.NoError(t, db.First(&before, "cluster_name = ?", "c1").Error)

	time.Sleep(20 * time.Millisecond)
	e.HeartbeatTick()

	var after database.LeaderLease
	require.NoError(t, db.First(&after, "cluster_name = ?", "c1").Error)
	assert.True(t, after.LeaseUntil.After(*before.LeaseUntil), "lease extended")

	// Node no longer owns the lease (stolen) → heartbeat drops leadership.
	require.NoError(t, db.Exec(
		"UPDATE leader_lease SET leader_node_id = 'nodeB' WHERE cluster_name = ?", "c1").Error)
	e.HeartbeatTick()
	assert.False(t, e.IsLeader(), "rowcount==0 → defensive release")
}

func TestStopReleasesLease(t *testing.T) {
	db := newElectorDB(t)
	a := New(db, "nodeA", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, a.Join())
	a.ElectTick()
	require.True(t, a.IsLeader())
	a.Stop()

	assert.False(t, a.IsLeader())
	var lease database.LeaderLease
	require.NoError(t, db.First(&lease, "cluster_name = ?", "c1").Error)
	assert.Nil(t, lease.LeaderNodeID, "lease released")
	var node database.ClusterNode
	require.NoError(t, db.First(&node, "node_id = ?", "nodeA").Error)
	assert.Equal(t, "left", node.Role)

	// Standby takes over immediately after release.
	b := New(db, "nodeB", "c1", 3*time.Second, 10*time.Second)
	require.NoError(t, b.Join())
	b.ElectTick()
	assert.True(t, b.IsLeader())
}

func TestPeersExcludesSelfAndStale(t *testing.T) {
	db := newElectorDB(t)
	a := New(db, "nodeA", "c1", time.Second, 10*time.Second)
	require.NoError(t, a.Join())
	b := New(db, "nodeB", "c1", time.Second, 10*time.Second)
	require.NoError(t, b.Join())

	assert.Equal(t, []string{"nodeB"}, a.Peers())

	// B's heartbeat goes stale → no longer a peer.
	stale := database.NowUTC().Add(-time.Minute)
	require.NoError(t, db.Exec(
		"UPDATE cluster_nodes SET last_heartbeat = ? WHERE node_id = ?", stale, "nodeB").Error)
	assert.Empty(t, a.Peers())
}

func TestDefaultNodeIDUsesHostnameAndPID(t *testing.T) {
	db := newElectorDB(t)
	e := New(db, "", "c1", time.Second, 10*time.Second)
	require.NoError(t, e.Join())
	assert.Contains(t, e.NodeID, "-")
	assert.NotEmpty(t, e.NodeID)
}
