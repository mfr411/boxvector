package web

import "testing"

func TestParseStartupConf(t *testing.T) {
	kv, on, off := parseStartupConf("# c\nunix {\n  nodaemon\n  cli-listen /run/vpp/cli.sock\n}\ncpu {\n  main-core 0\n  corelist-workers 1-3\n}\nmemory { main-heap-size 512M }\nplugins {\n  plugin default { enable }\n  plugin linux_cp_plugin.so { enable }\n  plugin dpdk_plugin.so { disable }\n}\n")
	for k, v := range map[string]string{"cpu.main-core": "0", "cpu.corelist-workers": "1-3", "memory.main-heap-size": "512M", "unix.cli-listen": "/run/vpp/cli.sock"} {
		if kv[k] != v {
			t.Errorf("%s = %q, want %q", k, kv[k], v)
		}
	}
	if len(off) != 1 || off[0] != "dpdk_plugin.so" {
		t.Errorf("off = %v", off)
	}
	_ = on
}

func TestFibCount(t *testing.T) {
	out := "ipv4-VRF:0, fib_index:0, flow hash:[src dst]\n    Prefix length         Count\n                   0               1\n                  24             100\n                  32               5\n"
	if n := fibCount(out); n != 106 {
		t.Errorf("fibCount = %d, want 106", n)
	}
}
