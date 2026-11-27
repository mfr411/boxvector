package web

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Read-only VPP settings: what startup.conf asks for next to what VPP is actually using.

type vppSetting struct {
	Group string `json:"group"`
	Key   string `json:"key"`
	Conf  string `json:"conf"`
	Live  string `json:"live"`
}

type vppSettings struct {
	Available bool         `json:"available"`
	Running   bool         `json:"running"`
	Rows      []vppSetting `json:"rows"`
	Plugins   []string     `json:"plugins"`
	Conf      string       `json:"conf"`
	Warn      []string     `json:"warn"`
}

const vppStartup = "/etc/vpp/startup.conf"

// parseStartupConf flattens "section { key value }" into "section.key" -> value.
func parseStartupConf(s string) (map[string]string, []string, []string) {
	kv := map[string]string{}
	var on, off []string
	section := ""
	depth := 0
	for _, raw := range strings.Split(s, "\n") {
		l := strings.TrimSpace(raw)
		if i := strings.Index(l, "#"); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		if l == "" {
			continue
		}
		// one-line form: memory { main-heap-size 512M }
		if i := strings.Index(l, "{"); i > 0 && strings.HasSuffix(l, "}") && depth == 0 {
			if sec := strings.TrimSpace(l[:i]); len(strings.Fields(sec)) == 1 {
				parseConfBody(sec, strings.TrimSpace(l[i+1:len(l)-1]), kv, &on, &off)
				continue
			}
		}
		if strings.HasSuffix(l, "{") && depth == 0 {
			section = strings.TrimSpace(strings.TrimSuffix(l, "{"))
			depth++
			continue
		}
		if l == "}" {
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				section = ""
			}
			continue
		}
		if depth == 1 && section != "" {
			parseConfBody(section, l, kv, &on, &off)
		}
	}
	return kv, on, off
}

func parseConfBody(sec, body string, kv map[string]string, on, off *[]string) {
	f := strings.Fields(body)
	if len(f) == 0 {
		return
	}
	if sec == "plugins" && f[0] == "plugin" && len(f) >= 3 {
		if f[len(f)-1] == "enable" || (len(f) > 3 && f[len(f)-2] == "enable") {
			*on = append(*on, f[1])
		} else {
			*off = append(*off, f[1])
		}
		return
	}
	kv[sec+"."+f[0]] = strings.Join(f[1:], " ")
}

var reHeap = regexp.MustCompile(`total:\s*([0-9.]+)([KMG]?),\s*used:\s*([0-9.]+)([KMG]?),\s*free:\s*([0-9.]+)([KMG]?)`)

func meminfoKB(k string) int {
	b, _ := os.ReadFile("/proc/meminfo")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, k+":") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				n, _ := strconv.Atoi(f[1])
				return n
			}
		}
	}
	return 0
}

func fibCount(out string) int {
	n := 0
	in := false
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Prefix length") {
			in = true
			continue
		}
		if f := strings.Fields(l); in && len(f) == 2 {
			if a, e1 := strconv.Atoi(f[0]); e1 == nil {
				_ = a
				c, _ := strconv.Atoi(f[1])
				n += c
			}
		}
		if strings.HasPrefix(l, "ipv") {
			in = false
		}
	}
	return n
}

func readVPPSettings() vppSettings {
	st := vppSettings{Available: vppInstalled(), Rows: []vppSetting{}, Plugins: []string{}, Warn: []string{}}
	if !st.Available {
		return st
	}
	b, _ := os.ReadFile(vppStartup)
	st.Conf = string(b)
	kv, on, off := parseStartupConf(st.Conf)
	add := func(g, k, c, l string) { st.Rows = append(st.Rows, vppSetting{g, k, c, l}) }
	get := func(k string) string {
		if v := kv[k]; v != "" {
			return v
		}
		return "default"
	}
	add("CPU", "main core", get("cpu.main-core"), "")
	add("CPU", "worker cores", get("cpu.corelist-workers"), "")
	add("Memory", "main heap", get("memory.main-heap-size"), "")
	add("Memory", "stats segment", get("statseg.size"), "")
	add("Buffers", "buffers per NUMA", get("buffers.buffers-per-numa"), "")
	add("Buffers", "page size", get("buffers.page-size"), "")
	add("Control", "CLI socket", get("unix.cli-listen"), "")
	add("Control", "startup config", get("unix.startup-config"), "")
	add("Control", "API group", get("api-segment.gid"), "")
	if len(off) > 0 {
		add("Plugins", "disabled", strings.Join(off, ", "), "")
	}
	hpT, hpF := meminfoKB("HugePages_Total"), meminfoKB("HugePages_Free")
	add("Memory", "hugepages (2 MB)", "", strconv.Itoa(hpT-hpF)+" used / "+strconv.Itoa(hpT)+" total")
	if o, err := vppExec("show memory main-heap"); err == nil {
		st.Running = true
		if m := reHeap.FindStringSubmatch(o); m != nil {
			add("Memory", "main heap in use", "", m[3]+m[4]+" of "+m[1]+m[2]+" ("+m[5]+m[6]+" free)")
			if u, _ := strconv.ParseFloat(m[3], 64); u > 0 {
				t, _ := strconv.ParseFloat(m[1], 64)
				if t > 0 && u/t > 0.85 {
					st.Warn = append(st.Warn, "Main heap is over 85% used. A bigger FIB or vppcfg apply can crash VPP: raise main-heap-size and restart VPP.")
				}
			}
		}
	}
	if st.Running {
		v4, _ := vppExec("show ip fib summary")
		v6, _ := vppExec("show ip6 fib summary")
		add("Routing", "FIB prefixes", "", "IPv4 "+strconv.Itoa(fibCount(v4))+" · IPv6 "+strconv.Itoa(fibCount(v6)))
		if o, err := vppExec("show plugins"); err == nil {
			for _, l := range strings.Split(o, "\n") {
				f := strings.Fields(l)
				if len(f) >= 3 && strings.HasSuffix(f[1], ".so") {
					st.Plugins = append(st.Plugins, strings.TrimSuffix(f[1], ".so"))
				}
			}
		}
		_ = on
	}
	if _, err := os.Stat("/opt/vppcfg-venv/bin/vppcfg"); err != nil {
		st.Warn = append(st.Warn, "vppcfg is not installed: the Interfaces editor is read-only. Re-run install.sh --mode custom --vpp.")
	}
	return st
}

func (s *server) vppSettingsAPI(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(readVPPSettings())
}
