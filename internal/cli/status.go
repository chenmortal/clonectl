package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "检查 rclone rcd 进程与主服务运行状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// rclone rcd via RC API probe.
		manager := rclone.NewManager(
			rclone.RCNormal(cfg.RcloneRCURL), cfg.RcloneRCAddr,
			cfg.RcloneRCUser, cfg.RcloneRCPass, cfg.RcloneBin)
		if manager.IsRunning() {
			fmt.Printf("rclone rcd: running (%s)\n", cfg.RcloneRCURL)
		} else {
			fmt.Println("rclone rcd: stopped")
		}

		// Service via /healthz.
		host := cfg.APIHost
		if host == "0.0.0.0" || host == "::" || host == "" {
			host = "127.0.0.1"
		}
		base := fmt.Sprintf("http://%s:%d", host, cfg.APIPort)
		health, err := fetchHealthz(base)
		if err != nil {
			fmt.Printf("rclone-sync service: stopped (%s/healthz unreachable)\n", base)
		} else {
			fmt.Printf("rclone-sync service: running (%s/healthz, rclone_reachable=%v)\n",
				base, health["rclone_reachable"])
		}

		// Cluster section read straight from the DB (works even when the
		// service is down).
		dialect, dsn, err := config.ParseDatabaseURL(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		db, err := database.Open(dialect, dsn)
		if err != nil {
			return err
		}
		sqlDB, _ := db.DB()
		defer sqlDB.Close()

		var lease struct {
			LeaderNodeID *string
		}
		if err := db.Table("leader_lease").
			Select("leader_node_id").
			Where("cluster_name = ?", cfg.ClusterName).
			Take(&lease).Error; err == nil && lease.LeaderNodeID != nil {
			fmt.Printf("cluster: leader=%s (name=%s)\n", *lease.LeaderNodeID, cfg.ClusterName)
		} else {
			fmt.Printf("cluster: no leader (name=%s)\n", cfg.ClusterName)
		}

		cutoff := database.NowUTC().Add(-time.Duration(maxInt(2*cfg.HeartbeatInterval, cfg.LeaseDuration)) * time.Second)
		type nodeRow struct {
			NodeID string
			Role   string
		}
		var nodes []nodeRow
		db.Table("cluster_nodes").
			Select("node_id, role").
			Where("cluster_name = ? AND left_at IS NULL AND last_heartbeat >= ?",
				cfg.ClusterName, cutoff).
			Order("node_id").Scan(&nodes)
		fmt.Printf("cluster members (%d live):", len(nodes))
		for _, n := range nodes {
			fmt.Printf(" %s(%s)", n.NodeID, n.Role)
		}
		fmt.Println()

		if health != nil {
			if nid, ok := health["node_id"].(string); ok && nid != "" {
				fmt.Printf("local node_id: %s\n", nid)
			}
		}
		return nil
	},
}

func fetchHealthz(base string) (map[string]any, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
