package sshclient

import (
	"context"
	"strings"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// collectTimeout bounds one metric refresh.
const collectTimeout = 25 * time.Second

const shSectionBegin = "###SEXTANT-"

// Collect gathers dashboard metrics. Every field is optional: a host without
// `free`, docker or nvidia-smi reports those as N/A instead of failing.
func Collect(ctx context.Context, client *Client, host model.Host) model.Metrics {
	ctx, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()

	uname, _, _, err := client.Exec(ctx, "uname -s && uname -r && hostname", 8*time.Second)
	if err != nil {
		// Windows OpenSSH: no uname, but PowerShell is always there.
		return collectWindows(ctx, client, host)
	}
	info := strings.Fields(strings.TrimSpace(string(uname)))
	if len(info) == 0 {
		return collectWindows(ctx, client, host)
	}
	switch info[0] {
	case "Linux":
		return collectUnix(ctx, client, host, linuxScript)
	case "Darwin":
		return collectUnix(ctx, client, host, darwinScript)
	case "FreeBSD", "OpenBSD", "NetBSD":
		return collectUnix(ctx, client, host, bsdScript)
	default:
		return collectUnix(ctx, client, host, linuxScript)
	}
}

func collectUnix(ctx context.Context, client *Client, host model.Host, script string) model.Metrics {
	m := model.Metrics{CollectedAt: time.Now(), Hostname: host.Name}
	out, _, _, err := client.Exec(ctx, script, collectTimeout)
	if err != nil && len(out) == 0 {
		m.Error = err.Error()
		return m
	}
	sections := splitSections(string(out))

	if sys := strings.Fields(sections["SYS"]); len(sys) >= 1 {
		switch sys[0] {
		case "Linux":
			m.OS = "Linux"
		case "Darwin":
			m.OS = "macOS"
		default:
			m.OS = sys[0]
		}
		if len(sys) >= 2 {
			m.Kernel = sys[1]
		}
		if len(sys) >= 3 {
			m.Hostname = sys[2]
		}
	}
	if pretty := strings.TrimSpace(sections["OSREL"]); pretty != "" {
		if _, value, ok := strings.Cut(pretty, "="); ok {
			m.OS = strings.Trim(value, `"`)
		}
	}

	m.CPUPercent = ParseCPUTop(sections["CPU"])
	if m.CPUPercent == nil {
		m.CPUPercent = ParseCPUMacOS(sections["CPU"])
	}
	if m.CPUPercent == nil {
		m.CPUPercent = ParseCPUProcStat(sections["STAT"])
	}
	if m.RAMPercent == nil {
		m.RAMPercent = ParseRAMFree(sections["MEM"])
	}
	if m.RAMPercent == nil {
		m.RAMPercent = ParseRAMProcMeminfo(sections["MEMINFO"])
	}
	if m.RAMPercent == nil {
		m.RAMPercent = ParseRAMVmstat(sections["VMSTAT"], sections["MEMSIZE"])
	}
	if m.RAMPercent == nil {
		m.RAMPercent = ParseRAMSysctl(sections["SYSCTL"])
	}
	m.SwapPercent = ParseSwapFree(sections["MEM"])
	m.DiskPercent = ParseDiskDF(sections["DISK"])
	if up := ParseUptime(sections["UPTIME"]); up != "" {
		m.Uptime = up
	}
	m.Load = ParseLoadavg(sections["LOADAVG"])
	if m.Load == "" {
		m.Load = ParseLoadavgUptime(sections["UPTIME"])
	}
	m.Processes = ParseTopProcesses(sections["PS"])
	m.GPUs = ParseNVIDIASMI(sections["GPU"])
	if m.GPUs == nil {
		m.GPUs = ParseRocmSMI(sections["ROCM"])
	}
	dockerUp, dockerAll := ParseDockerCounts(sections["DOCKERUP"], sections["DOCKERALL"])
	m.DockerUp, m.DockerAll = dockerUp, dockerAll
	return m
}

func collectWindows(ctx context.Context, client *Client, host model.Host) model.Metrics {
	m := model.Metrics{CollectedAt: time.Now(), Hostname: host.Name, OS: "Windows"}
	out, _, _, err := client.Exec(ctx, windowsScript, collectTimeout)
	if err != nil && len(out) == 0 {
		m.Error = err.Error()
		return m
	}
	if parsed, ok := ParseWindowsMetrics(string(out)); ok {
		parsed.CollectedAt = m.CollectedAt
		if parsed.Hostname == "" {
			parsed.Hostname = host.Name
		}
		return parsed
	}
	m.Error = "windows collector returned unparseable output"
	return m
}

func splitSections(output string) map[string]string {
	sections := map[string]string{}
	current := ""
	var buf strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		sections[current] = buf.String()
		buf.Reset()
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, shSectionBegin) {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, shSectionBegin))
			continue
		}
		if current == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	flush()
	return sections
}

// section renders a marker the parser recognises. The marker is quoted: an
// unquoted leading # starts a shell comment, so `echo ###SEXTANT-SYS` prints an
// empty line and every metric section disappears.
func section(name string) string { return "echo '" + shSectionBegin + name + "'\n" }

var linuxScript = section("SYS") + `
uname -s; uname -r; hostname
` + section("OSREL") + `
grep PRETTY_NAME /etc/os-release 2>/dev/null | head -1
` + section("CPU") + `
LC_ALL=C top -bn1 2>/dev/null | head -20
` + section("STAT") + `
cat /proc/stat 2>/dev/null | head -1
` + section("MEM") + `
free -b 2>/dev/null
` + section("MEMINFO") + `
cat /proc/meminfo 2>/dev/null | head -8
` + section("DISK") + `
df -k / 2>/dev/null
` + section("UPTIME") + `
LC_ALL=C uptime 2>/dev/null
` + section("LOADAVG") + `
cat /proc/loadavg 2>/dev/null
` + section("PS") + `
ps -eo pcpu,pmem,comm --sort=-pcpu 2>/dev/null | head -4
` + section("DOCKERUP") + `
docker ps -q 2>/dev/null | wc -l
` + section("DOCKERALL") + `
docker ps -aq 2>/dev/null | wc -l
` + section("GPU") + `
command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi --query-gpu=name,utilization.gpu,memory.used,memory.total --format=csv,noheader,nounits 2>/dev/null
` + section("ROCM") + `
command -v rocm-smi >/dev/null 2>&1 && rocm-smi --showuse --showmeminfo vram --showproductname --csv 2>/dev/null
` + section("END")

var darwinScript = section("SYS") + `
uname -s; uname -r; hostname
` + section("CPU") + `
LC_ALL=C top -l 1 -n 0 2>/dev/null | head -20
` + section("VMSTAT") + `
vm_stat 2>/dev/null
` + section("MEMSIZE") + `
sysctl hw.memsize 2>/dev/null
` + section("DISK") + `
df -k / 2>/dev/null
` + section("UPTIME") + `
LC_ALL=C uptime 2>/dev/null
` + section("PS") + `
ps -A -o pcpu,pmem,comm 2>/dev/null | sort -rn | head -4
` + section("DOCKERUP") + `
docker ps -q 2>/dev/null | wc -l
` + section("DOCKERALL") + `
docker ps -aq 2>/dev/null | wc -l
` + section("END")

var bsdScript = section("SYS") + `
uname -s; uname -r; hostname
` + section("CPU") + `
LC_ALL=C top -b -d1 2>/dev/null | head -20 || LC_ALL=C top -n 1 2>/dev/null | head -20
` + section("SYSCTL") + `
sysctl vm.stats.vm.v_page_count hw.pagesize vm.stats.vm.v_free_count vm.stats.vm.v_inactive_count vfs.bufspace kstat.zfs.misc.arcstats.size kstat.zfs.misc.arcstats.c_min 2>/dev/null
` + section("DISK") + `
df -k / 2>/dev/null
` + section("UPTIME") + `
LC_ALL=C uptime 2>/dev/null
` + section("PS") + `
ps -axo pcpu,pmem,comm 2>/dev/null | sort -rn | head -4
` + section("END")

// windowsScript collects the same fields through PowerShell/CIM.
const windowsScript = `powershell -NoProfile -Command "$ErrorActionPreference='SilentlyContinue'; ` +
	`$os=Get-CimInstance Win32_OperatingSystem; ` +
	`$cpu=(Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average; ` +
	`$disk=Get-CimInstance Win32_LogicalDisk -Filter \"DeviceID='C:'\"; ` +
	`$up=(New-TimeSpan -Start $os.LastBootUpTime).TotalSeconds; ` +
	`$procs=Get-Process | Sort-Object CPU -Descending | Select-Object -First 3 @{n='name';e={$_.ProcessName}},@{n='cpu';e={$_.CPU}},@{n='mem';e={$_.WorkingSet64}}; ` +
	`$gpus=Get-CimInstance Win32_VideoController | Select-Object @{n='name';e={$_.Name}}; ` +
	`[pscustomobject]@{cpu=$cpu;mem_total=$os.TotalVisibleMemorySize*1024;mem_avail=$os.FreePhysicalMemory*1024;` +
	`disk_total=$disk.Size;disk_free=$disk.FreeSpace;uptime_seconds=$up;os=$os.Caption;version=$os.Version;` +
	`hostname=$os.CSName;processes=$procs;gpus=$gpus} | ConvertTo-Json -Depth 4 -Compress"`
