package sshclient

import (
	"strings"
	"testing"
)

func almost(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %v", want)
	}
	if diff := *got - want; diff > 0.05 || diff < -0.05 {
		t.Fatalf("got %v, want %v", *got, want)
	}
}

func TestNormalizeDecimalCommas(t *testing.T) {
	cases := map[string]string{
		"2,3 us":     "2.3 us",
		"a,b":        "a,b",
		"1,234,567":  "1.234.567",
		"no commas":  "no commas",
		",5 leading": ",5 leading",
		"trailing1,": "trailing1,",
	}
	for in, want := range cases {
		if got := normalizeDecimalCommas(in); got != want {
			t.Errorf("normalizeDecimalCommas(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCPUTopProcps(t *testing.T) {
	top := `top - 15:00:00 up 1 day,  2 users,  load average: 0.10, 0.20, 0.30
Tasks: 100 total,   1 running,  99 sleeping
%Cpu(s):  2.3 us,  0.7 sy,  0.0 ni, 96.7 id,  0.3 wa,  0.0 hi,  0.0 si,  0.0 st
MiB Mem :   8000.0 total`
	almost(t, ParseCPUTop(top), 3.3)
}

func TestParseCPUTopBusybox(t *testing.T) {
	top := `Mem: 12345K used, 6789K free, 0K shrd, 0K buff, 0K cached
CPU:   2% usr   1% sys   0% nic  96% idle   0% io   0% irq   0% sirq
Load average: 0.10 0.20 0.30`
	almost(t, ParseCPUTop(top), 4)
}

func TestParseCPUTopOldProcps(t *testing.T) {
	top := `top - 15:00:00 up 1 day
Cpu(s):  3,0%us,  2,0%sy,  0,0%ni, 95,0%id,  0,0%wa`
	almost(t, ParseCPUTop(top), 5)
}

func TestParseCPUMacOS(t *testing.T) {
	out := `Processes: 500 total, 2 running
CPU usage: 5.26% user, 10.52% sys, 84.21% idle`
	almost(t, ParseCPUMacOS(out), 15.79)
}

func TestParseCPUProcStat(t *testing.T) {
	almost(t, ParseCPUProcStat("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 ..."), 20)
	if ParseCPUProcStat("nothing here") != nil {
		t.Fatal("expected nil for junk")
	}
}

func TestParseRAMFree(t *testing.T) {
	out := `               total        used        free      shared  buff/cache   available
Mem:     8000000000  2000000000  1000000000           0  5000000000  6000000000
Swap:    2000000000   500000000  1500000000`
	almost(t, ParseRAMFree(out), 25)
	almost(t, ParseSwapFree(out), 25)
}

func TestParseRAMFreeOldLayout(t *testing.T) {
	// Older `free` has no "available" column.
	out := `             total       used       free     shared    buffers     cached
Mem:    8000000000 2000000000 1000000000          0          0          0`
	almost(t, ParseRAMFree(out), 25)
}

func TestParseRAMProcMeminfo(t *testing.T) {
	out := `MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    6000000 kB`
	almost(t, ParseRAMProcMeminfo(out), 25)
}

func TestParseRAMVmstat(t *testing.T) {
	vm := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                          100000.
Pages speculative:                    50000.
Pages active:                        500000.`
	almost(t, ParseRAMVmstat(vm, "hw.memsize: 16000000000"), 84.6)
}

func TestParseDiskDF(t *testing.T) {
	out := `Filesystem     1K-blocks      Used Available Use% Mounted on
/dev/sda1       50000000  40000000   8000000  84% /`
	almost(t, ParseDiskDF(out), 84)

	busybox := `Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/sda1              50000000  40000000   8000000  84% /`
	almost(t, ParseDiskDF(busybox), 84)
}

func TestParseUptimeAndLoad(t *testing.T) {
	out := ` 15:04:05 up 3 days,  4:20,  2 users,  load average: 0.10, 0.20, 0.30`
	if got := ParseUptime(out); got != "3 days" {
		t.Errorf("uptime = %q", got)
	}
	if got := ParseLoadavgUptime(out); got != "0.10 0.20 0.30" {
		t.Errorf("load = %q", got)
	}
	if got := ParseLoadavg("0.10 0.20 0.30 1/234 5678"); got != "0.10 0.20 0.30" {
		t.Errorf("loadavg = %q", got)
	}
}

func TestParseTopProcesses(t *testing.T) {
	out := ` 12.5  3.4 nginx
  8.0  1.2 postgres
  1.0  0.5 systemd
  0.1  0.1 bash`
	got := ParseTopProcesses(out)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "nginx" || got[0].CPUPercent != 12.5 || got[0].MemPercent != 3.4 {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestParseNVIDIASMI(t *testing.T) {
	out := `NVIDIA GeForce RTX 4090, 45, 2048, 24564
NVIDIA A100, 99, 40000, 40960`
	got := ParseNVIDIASMI(out)
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "NVIDIA GeForce RTX 4090" || got[0].UtilPct != 45 {
		t.Fatalf("first = %+v", got[0])
	}
	if got[0].MemoryTot != 24564*1024*1024 {
		t.Fatalf("total bytes = %d", got[0].MemoryTot)
	}
	if got[1].UtilPct != 99 {
		t.Fatalf("second util = %v", got[1].UtilPct)
	}
}

func TestParseRocmSMI(t *testing.T) {
	out := `device,GPU use (%)
card0,45
card1,90
device,VRAM Total Memory (B),VRAM Total Used Memory (B)
card0,17163091968,2147483648
card1,17163091968,8589934592
device,Card Series
card0,Radeon RX 7900 XTX
card1,Radeon RX 7900 XTX`
	got := ParseRocmSMI(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, got %+v", len(got), got)
	}
	if got[0].UtilPct != 45 || got[1].UtilPct != 90 {
		t.Fatalf("util = %+v", got)
	}
	if got[0].MemoryUsed != 2147483648 || got[0].MemoryTot != 17163091968 {
		t.Fatalf("memory = %+v", got[0])
	}
	if got[1].MemoryPct < 49 || got[1].MemoryPct > 51 {
		t.Fatalf("memory pct = %v", got[1].MemoryPct)
	}
	if got[0].Name != "Radeon RX 7900 XTX" {
		t.Fatalf("name = %q", got[0].Name)
	}
}

func TestParseRocmSMINoHeader(t *testing.T) {
	out := `card0,25
card1,50`
	got := ParseRocmSMI(out)
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "card0" {
		t.Fatalf("name = %q", got[0].Name)
	}
	if got[0].UtilPct != 0 {
		t.Fatalf("util without header should stay 0, got %v", got[0].UtilPct)
	}
}

func TestParseDockerCounts(t *testing.T) {
	up, all := ParseDockerCounts("3\n", "7\n")
	if up == nil || *up != 3 || all == nil || *all != 7 {
		t.Fatalf("up=%v all=%v", up, all)
	}
	if p, q := ParseDockerCounts("", ""); p != nil || q != nil {
		t.Fatalf("empty should be nil: %v %v", p, q)
	}
}

func TestParseWindowsMetrics(t *testing.T) {
	out := `some noise
{"cpu":25.5,"mem_total":16000000000,"mem_avail":8000000000,"disk_total":500000000000,"disk_free":100000000000,"uptime_seconds":90061,"os":"Microsoft Windows 11 Pro","version":"10.0.22631","hostname":"WINBOX","processes":[{"name":"chrome","cpu":30,"mem":2},{"name":"code","cpu":20,"mem":3},{"name":"svchost","cpu":10,"mem":1},{"name":"extra","cpu":1,"mem":1}],"gpus":[{"name":"NVIDIA RTX 3080","util":40,"used":4000000000,"total":10000000000}]}`
	m, ok := ParseWindowsMetrics(out)
	if !ok {
		t.Fatal("parse failed")
	}
	if m.CPUPercent == nil || *m.CPUPercent != 25.5 {
		t.Fatalf("cpu = %v", m.CPUPercent)
	}
	almost(t, m.RAMPercent, 50)
	almost(t, m.DiskPercent, 80)
	if m.Uptime != "1 days" {
		t.Fatalf("uptime = %q", m.Uptime)
	}
	if len(m.Processes) != 3 {
		t.Fatalf("processes = %d", len(m.Processes))
	}
	if len(m.GPUs) != 1 || m.GPUs[0].UtilPct != 40 {
		t.Fatalf("gpus = %+v", m.GPUs)
	}
	almost(t, &m.GPUs[0].MemoryPct, 40)
	if m.OS != "Microsoft Windows 11 Pro" {
		t.Fatalf("os = %q", m.OS)
	}
}

func TestMetricScriptsQuoteSectionMarkers(t *testing.T) {
	scripts := map[string]struct {
		script  string
		markers []string
	}{
		"linux": {
			script: linuxScript,
			markers: []string{
				"SYS", "OSREL", "CPU", "STAT", "MEM", "MEMINFO", "DISK",
				"UPTIME", "LOADAVG", "PS", "DOCKERUP", "DOCKERALL", "GPU",
				"ROCM", "END",
			},
		},
		"darwin": {
			script: darwinScript,
			markers: []string{
				"SYS", "CPU", "VMSTAT", "MEMSIZE", "DISK", "UPTIME", "PS",
				"DOCKERUP", "DOCKERALL", "END",
			},
		},
		"bsd": {
			script: bsdScript,
			markers: []string{
				"SYS", "CPU", "SYSCTL", "DISK", "UPTIME", "PS", "END",
			},
		},
	}
	for name, tc := range scripts {
		if strings.Contains(tc.script, "echo ###SEXTANT") {
			t.Errorf("%s: unquoted marker is a shell comment: %q", name, tc.script)
		}
		for _, marker := range tc.markers {
			want := "echo '###SEXTANT-" + marker + "'"
			if !strings.Contains(tc.script, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

func TestSplitSections(t *testing.T) {
	out := "###SEXTANT-SYS\nLinux\n6.1.0\nhost1\n###SEXTANT-CPU\ncpu line\n###SEXTANT-END\n"
	s := splitSections(out)
	if got := s["SYS"]; got != "Linux\n6.1.0\nhost1\n" {
		t.Fatalf("SYS = %q", got)
	}
	if got := s["CPU"]; got != "cpu line\n" {
		t.Fatalf("CPU = %q", got)
	}
}
