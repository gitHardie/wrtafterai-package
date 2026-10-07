package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRegisterAllListsAllTools 走真实 mcp.Server 注册 + 内存传输列举，
// 验证 25 个工具（批1 12 个 + 批2 13 个）的 input/output struct 能通过 jsonschema 反射。
func TestRegisterAllListsAllTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	RegisterAll(server, &Deps{})

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, st) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	want := []string{
		// 批1
		"router.system_info", "router.network_interfaces", "router.wan_status",
		"router.wireless_clients", "router.connected_devices", "router.connection_stats",
		"router.dhcp_leases", "router.realtime_traffic", "router.traffic_by_device",
		"router.firewall_rules", "router.system_log", "router.speed_test",
		// 批2
		"router.system_resources", "router.disk_usage", "router.top_processes",
		"router.kernel_log", "router.public_ip", "router.dns_resolve",
		"router.dns_config", "router.routes", "router.ping",
		"router.service_status", "router.docker_containers",
		"router.get_config", "router.wireless_config",
	}
	if len(res.Tools) != len(want) {
		t.Fatalf("tool count = %d, want %d: %v", len(res.Tools), len(want), keys(got))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
