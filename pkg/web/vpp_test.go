package web

import "testing"

func TestParseVPP(t *testing.T) {
	th := parseVPPThreads("ID   Name    LWP  CPU   Sched    Loops   Epolls Sleeps FdWakeups WakeReq Wakeups\n 0 vpp_main 576   0 other (0)  3904559 3903536\n 1 vpp_wk_0 592   1 other (0) 14032279   16466\n")
	if len(th) != 2 || th[1].Name != "vpp_wk_0" || th[1].CPU != 1 {
		t.Fatalf("threads: %+v", th)
	}
	ifs := parseVPPIfaces("              Name               Idx    State  MTU (L3/IP4/IP6/MPLS)     Counter          Count     \nTenGigabitEthernet3/0/0           1      up          9000/0/0/0     rx packets                  1500\n                                                                    rx bytes                  900000\n                                                                    tx packets                  1400\n                                                                    drops                          7\nlocal0                            0     down          0/0/0/0       \n")
	if len(ifs) != 2 || !ifs[0].Up || ifs[0].RxP != 1500 || ifs[0].RxB != 900000 || ifs[0].TxP != 1400 || ifs[0].Drops != 7 || ifs[1].Up {
		t.Fatalf("ifaces: %+v", ifs)
	}
	l := parseVPPLCP("lcp default netns '<unset>'\nitf-pair: [0] loop0 tap4096 bvtest0 4 type tap\n")
	if len(l) != 1 || l[0].Host != "bvtest0" || l[0].Type != "tap" {
		t.Fatalf("lcp: %+v", l)
	}
	b := parseVPPBuffers("Pool Name            Index NUMA  Size  Data Size  Total  Avail  Cached   Used  \ndefault-numa-0         0     0   2304     2048    99119  99000     0       119\n")
	if len(b) != 1 || b[0].Total != 99119 || b[0].Used != 119 {
		t.Fatalf("buf: %+v", b)
	}
}

func TestReadVPPUnavailable(t *testing.T) {
	old := vppExec
	defer func() { vppExec = old }()
	vppExec = func(string) (string, error) { return "", errNoVPP }
	st := readVPP()
	if st.Running {
		t.Fatal("must not be running")
	}
}

var errNoVPP = &vppErr{}

type vppErr struct{}

func (*vppErr) Error() string { return "no vpp" }

func TestVPPAnsiStripped(t *testing.T) {
	in := "\x1b[33mID\x1b[0m \x1b[33m  Name  \x1b[0m \x1b[33mLWP\x1b[0m \x1b[33mCPU\x1b[0m\n 1\x1b[0m vpp_wk_0\x1b[0m 592\x1b[0m   1\x1b[0m other (0)\x1b[0m\n"
	th := parseVPPThreads(ansiRe.ReplaceAllString(in, ""))
	if len(th) != 1 || th[0].CPU != 1 || th[0].Name != "vpp_wk_0" {
		t.Fatalf("%+v", th)
	}
}
