package sshclient

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/coddan/sextant/internal/model"
)

// normalizeDecimalCommas rewrites "99,1" as "99.1" for hosts that ignore the
// pinned locale. Only a comma between two digits is a radix point.
func normalizeDecimalCommas(line string) string {
	runes := []rune(line)
	for i, r := range runes {
		if r != ',' || i == 0 || i+1 >= len(runes) {
			continue
		}
		if isDigit(runes[i-1]) && isDigit(runes[i+1]) {
			runes[i] = '.'
		}
	}
	return string(runes)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// ParseCPUTop reads CPU usage from `top -bn1` output (Linux, busybox, old
// procps) and returns the used percentage.
func ParseCPUTop(output string) *float64 {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CPU:") {
			return parseCPUBusybox(trimmed)
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "%cpu") || strings.HasPrefix(lower, "cpu(s)") {
			return parseCPULinuxTopLine(trimmed)
		}
	}
	return nil
}

func parseCPUBusybox(line string) *float64 {
	words := strings.Fields(line)
	for i := 0; i+1 < len(words); i++ {
		if words[i+1] == "idle" {
			if idle, err := strconv.ParseFloat(strings.TrimSuffix(words[i], "%"), 64); err == nil {
				return pct(100 - idle)
			}
		}
	}
	return nil
}

func parseCPULinuxTopLine(line string) *float64 {
	line = normalizeDecimalCommas(line)
	_, after, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	for _, field := range strings.Split(after, ",") {
		field = strings.TrimSpace(field)
		parts := strings.FieldsFunc(field, func(r rune) bool { return r == ' ' || r == '%' })
		if len(parts) != 2 {
			continue
		}
		label := strings.ToLower(strings.Trim(parts[1], ","))
		if label == "id" || label == "idle" {
			idle, err := strconv.ParseFloat(strings.TrimSuffix(parts[0], "%"), 64)
			if err != nil {
				return nil
			}
			return pct(100 - idle)
		}
	}
	return nil
}

// ParseCPUMacOS reads `top -l 1 -n 0` output.
func ParseCPUMacOS(output string) *float64 {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "cpu usage:") {
			continue
		}
		line = normalizeDecimalCommas(line)
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if !strings.HasSuffix(strings.ToLower(part), "idle") {
				continue
			}
			token := strings.Fields(part)
			if len(token) == 0 {
				continue
			}
			idle, err := strconv.ParseFloat(strings.TrimSuffix(token[0], "%"), 64)
			if err != nil {
				return nil
			}
			return pct(100 - idle)
		}
	}
	return nil
}

// ParseCPUProcStat reads /proc/stat (average since boot).
func ParseCPUProcStat(output string) *float64 {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
		return nil
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			break
		}
		values = append(values, v)
	}
	if len(values) < 4 {
		return nil
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	var total uint64
	for _, v := range values {
		total += v
	}
	if total == 0 {
		return nil
	}
	return pct(float64(total-idle) / float64(total) * 100)
}

// ParseRAMFree reads `free -b` (modern and busybox layouts). The available
// column is used when present; older `free` lacks it, so the used column is
// the fallback.
func ParseRAMFree(output string) *float64 {
	availableIdx := -1
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "Mem:") {
			if len(fields) < 4 {
				return nil
			}
			total, err := strconv.ParseFloat(fields[1], 64)
			if err != nil || total == 0 {
				return nil
			}
			if availableIdx >= 0 {
				idx := availableIdx
				// The header row has no leading label column, the data row does.
				if strings.HasSuffix(fields[0], ":") {
					idx++
				}
				if idx < len(fields) {
					if available, err := strconv.ParseFloat(fields[idx], 64); err == nil {
						return pct((total - available) / total * 100)
					}
				}
			}
			used, err := strconv.ParseFloat(fields[2], 64)
			if err != nil {
				return nil
			}
			return pct(used / total * 100)
		}
		// Header row: find the "available" column.
		for i, f := range fields {
			if strings.HasPrefix(f, "available") {
				availableIdx = i
			}
		}
	}
	return nil
}

// ParseSwapFree reads the Swap: line of `free -b`.
func ParseSwapFree(output string) *float64 {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Swap:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil
		}
		total, err1 := strconv.ParseFloat(fields[1], 64)
		used, err2 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil || total == 0 {
			if total == 0 {
				// A host with no swap reports zeros; show 0 rather than N/A.
				return pct(0)
			}
			return nil
		}
		return pct(used / total * 100)
	}
	return nil
}

// ParseRAMProcMeminfo is the fallback for hosts without `free`.
func ParseRAMProcMeminfo(output string) *float64 {
	var total, available float64
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		case "MemFree:":
			if available == 0 {
				available = v * 1024
			}
		}
	}
	if total == 0 {
		return nil
	}
	return pct((total - available) / total * 100)
}

// ParseRAMVmstat reads macOS `vm_stat` plus `sysctl hw.memsize`.
func ParseRAMVmstat(vmStat, memsize string) *float64 {
	total := parseSysctlInt(memsize, "hw.memsize")
	if total == 0 {
		return nil
	}
	pageSize := 4096.0
	if idx := strings.Index(vmStat, "page size of"); idx >= 0 {
		rest := vmStat[idx+len("page size of"):]
		if f := strings.Fields(rest); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				pageSize = v
			}
		}
	}
	var free, speculative float64
	for _, line := range strings.Split(vmStat, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "Pages free:"):
			free = parseVmstatValue(line)
		case strings.HasPrefix(strings.TrimSpace(line), "Pages speculative:"):
			speculative = parseVmstatValue(line)
		}
	}
	available := (free + speculative) * pageSize
	return pct((float64(total) - available) / float64(total) * 100)
}

func parseVmstatValue(line string) float64 {
	_, after, ok := strings.Cut(line, ":")
	if !ok {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(after), "."), 64)
	if err != nil {
		return 0
	}
	return v
}

// ParseRAMSysctl reads FreeBSD sysctl counters.
func ParseRAMSysctl(output string) *float64 {
	total := parseSysctlInt(output, "vm.stats.vm.v_page_count")
	pageSize := parseSysctlInt(output, "hw.pagesize")
	if total == 0 || pageSize == 0 {
		return nil
	}
	arc := parseSysctlInt(output, "kstat.zfs.misc.arcstats.size") - parseSysctlInt(output, "kstat.zfs.misc.arcstats.c_min")
	if arc < 0 {
		arc = 0
	}
	available := parseSysctlInt(output, "vm.stats.vm.v_free_count") +
		parseSysctlInt(output, "vm.stats.vm.v_inactive_count") +
		(parseSysctlInt(output, "vfs.bufspace")+int64(arc))/pageSize
	return pct(float64(int64(total)-available) / float64(total) * 100)
}

func parseSysctlInt(output, key string) int64 {
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

// ParseDiskDF returns the use percentage of the first data row.
func ParseDiskDF(output string) *float64 {
	lines := nonEmptyLines(output)
	if len(lines) < 2 {
		return nil
	}
	header := strings.Fields(lines[0])
	data := strings.Fields(lines[1])
	idx := -1
	for i, h := range header {
		if h == "Use%" || h == "Capacity" || strings.HasSuffix(h, "Use%") || strings.HasSuffix(h, "Capacity") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(data) {
		// Some busybox `df` prints no header; fall back to the only %-column.
		for i, f := range data {
			if strings.HasSuffix(f, "%") {
				v, err := strconv.ParseFloat(strings.TrimSuffix(f, "%"), 64)
				if err == nil {
					return pct(v)
				}
			}
			_ = i
		}
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(data[idx], "%"), 64)
	if err != nil {
		return nil
	}
	return pct(v)
}

// ParseUptime extracts the human readable uptime.
func ParseUptime(output string) string {
	line := firstLine(output)
	idx := strings.Index(strings.ToLower(line), " up ")
	if idx < 0 {
		return ""
	}
	after := strings.TrimSpace(line[idx+4:])
	var parts []string
	for _, part := range strings.Split(after, ", ") {
		if strings.Contains(strings.ToLower(part), "user") {
			break
		}
		parts = append(parts, strings.TrimSpace(part))
	}
	uptime := strings.Join(parts, ", ")
	if i := strings.Index(uptime, " days"); i >= 0 {
		return uptime[:i+5]
	}
	if i := strings.Index(uptime, " day,"); i >= 0 {
		return uptime[:i+4]
	}
	return uptime
}

// ParseLoadavg reads /proc/loadavg.
func ParseLoadavg(output string) string {
	fields := strings.Fields(firstLine(output))
	if len(fields) < 3 {
		return ""
	}
	return strings.Join(fields[:3], " ")
}

// ParseLoadavgUptime reads load averages from `uptime` output.
func ParseLoadavgUptime(output string) string {
	line := normalizeDecimalCommas(firstLine(output))
	_, tail, ok := strings.Cut(line, "load average")
	if !ok {
		return ""
	}
	_, tail, ok = strings.Cut(tail, ":")
	if !ok {
		return ""
	}
	var values []string
	for _, v := range strings.FieldsFunc(tail, func(r rune) bool { return r == ',' || r == ' ' }) {
		if v == "" {
			continue
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return ""
		}
		values = append(values, v)
		if len(values) == 3 {
			break
		}
	}
	if len(values) != 3 {
		return ""
	}
	return strings.Join(values, " ")
}

// ParseTopProcesses reads `ps` output of `%CPU %MEM COMMAND`, sorted remotely.
func ParseTopProcesses(output string) []model.ProcessInfo {
	var out []model.ProcessInfo
	for _, line := range strings.Split(output, "\n") {
		if len(out) == 3 {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		cpu, err1 := strconv.ParseFloat(normalizeDecimalCommas(fields[0]), 64)
		mem, err2 := strconv.ParseFloat(normalizeDecimalCommas(fields[1]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		name := strings.Join(fields[2:], " ")
		if name == "" {
			continue
		}
		out = append(out, model.ProcessInfo{Name: name, CPUPercent: cpu, MemPercent: mem})
	}
	return out
}

// ParseNVIDIASMI reads `nvidia-smi --query-gpu=name,utilization.gpu,memory.used,memory.total --format=csv,noheader,nounits`.
func ParseNVIDIASMI(output string) []model.GPUInfo {
	var out []model.GPUInfo
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		util, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		used, err1 := strconv.ParseFloat(fields[2], 64)
		total, err2 := strconv.ParseFloat(fields[3], 64)
		gpu := model.GPUInfo{Name: fields[0], UtilPct: util}
		if err1 == nil && err2 == nil && total > 0 {
			gpu.MemoryUsed = int64(used) * 1024 * 1024
			gpu.MemoryTot = int64(total) * 1024 * 1024
			gpu.MemoryPct = used / total * 100
		}
		out = append(out, gpu)
	}
	return out
}

// ParseRocmSMI reads AMD GPU data from `rocm-smi --csv` output. The tool
// prints one header row per requested table (use, memory, product name), so
// the parser remembers the most recent header and maps columns by name. This
// keeps it working across the rocm-smi versions that reorder or rename
// columns.
func ParseRocmSMI(output string) []model.GPUInfo {
	type gpuState struct {
		gpu  model.GPUInfo
		seen bool
	}
	order := []string{}
	state := map[string]*gpuState{}

	type header struct {
		use, used, total, name int
	}
	var hdr header
	resetHeader := func() { hdr = header{use: -1, used: -1, total: -1, name: -1} }
	resetHeader()

	get := func(id string) *gpuState {
		if s, ok := state[id]; ok {
			return s
		}
		s := &gpuState{}
		state[id] = s
		order = append(order, id)
		return s
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "===") {
			continue
		}
		fields := splitCSV(line)
		if len(fields) == 0 {
			continue
		}
		low := strings.ToLower(fields[0])
		if low == "device" || low == "gpu" {
			resetHeader()
			for i, f := range fields {
				col := strings.ToLower(f)
				switch {
				case hdr.use < 0 && strings.Contains(col, "use") && strings.Contains(col, "%"):
					hdr.use = i
				case hdr.used < 0 && strings.Contains(col, "used") && strings.Contains(col, "memory"):
					hdr.used = i
				case hdr.total < 0 && strings.Contains(col, "total") && strings.Contains(col, "memory"):
					hdr.total = i
				case hdr.name < 0 && (strings.Contains(col, "series") || strings.Contains(col, "model") || strings.Contains(col, "name")):
					hdr.name = i
				}
			}
			continue
		}
		if !strings.HasPrefix(low, "card") && !strings.HasPrefix(low, "gpu") {
			continue
		}
		s := get(fields[0])
		s.seen = true
		readPct := func(idx int) *float64 {
			if idx < 0 || idx >= len(fields) {
				return nil
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(fields[idx], "%")), 64)
			if err != nil {
				return nil
			}
			return pct(v)
		}
		readBytes := func(idx int) *float64 {
			if idx < 0 || idx >= len(fields) {
				return nil
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(fields[idx]), 64)
			if err != nil {
				return nil
			}
			return &v
		}
		if v := readPct(hdr.use); v != nil {
			s.gpu.UtilPct = *v
		}
		used := readBytes(hdr.used)
		total := readBytes(hdr.total)
		if used != nil {
			s.gpu.MemoryUsed = int64(*used)
		}
		if total != nil {
			s.gpu.MemoryTot = int64(*total)
		}
		if used != nil && total != nil && *total > 0 {
			s.gpu.MemoryPct = *used / *total * 100
		}
		if hdr.name >= 0 && hdr.name < len(fields) {
			if name := strings.TrimSpace(fields[hdr.name]); name != "" {
				s.gpu.Name = name
			}
		}
		if s.gpu.Name == "" {
			s.gpu.Name = fields[0]
		}
	}

	out := make([]model.GPUInfo, 0, len(order))
	for _, id := range order {
		if s := state[id]; s != nil && s.seen {
			out = append(out, s.gpu)
		}
	}
	return out
}

// splitCSV splits a single CSV row, honouring double quotes.
func splitCSV(line string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range line {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

// ParseDockerCounts reads the two `docker ps` line counts.
func ParseDockerCounts(running, all string) (*int, *int) {
	r := parseTrailingInt(running)
	a := parseTrailingInt(all)
	return r, a
}

func parseTrailingInt(s string) *int {
	for _, f := range strings.Fields(s) {
		if v, err := strconv.Atoi(f); err == nil {
			return &v
		}
	}
	return nil
}

// windowsMetrics is the JSON the PowerShell collector prints.
type windowsMetrics struct {
	CPU        *float64 `json:"cpu"`
	MemTotal   *float64 `json:"mem_total"`
	MemAvail   *float64 `json:"mem_avail"`
	DiskTotal  *float64 `json:"disk_total"`
	DiskFree   *float64 `json:"disk_free"`
	UptimeSecs *float64 `json:"uptime_seconds"`
	OS         string   `json:"os"`
	Version    string   `json:"version"`
	Hostname   string   `json:"hostname"`
	Processes  []struct {
		Name string   `json:"name"`
		CPU  *float64 `json:"cpu"`
		Mem  *float64 `json:"mem"`
	} `json:"processes"`
	GPUs []struct {
		Name  string   `json:"name"`
		Util  *float64 `json:"util"`
		Used  *float64 `json:"used"`
		Total *float64 `json:"total"`
	} `json:"gpus"`
}

// ParseWindowsMetrics reads the PowerShell JSON document.
func ParseWindowsMetrics(output string) (model.Metrics, bool) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return model.Metrics{}, false
	}
	var raw windowsMetrics
	if err := json.Unmarshal([]byte(output[start:end+1]), &raw); err != nil {
		return model.Metrics{}, false
	}
	m := model.Metrics{OS: raw.OS, Kernel: raw.Version, Hostname: raw.Hostname, CPUPercent: raw.CPU}
	if raw.MemTotal != nil && *raw.MemTotal > 0 && raw.MemAvail != nil {
		m.RAMPercent = pct((*raw.MemTotal - *raw.MemAvail) / *raw.MemTotal * 100)
	}
	if raw.DiskTotal != nil && *raw.DiskTotal > 0 && raw.DiskFree != nil {
		m.DiskPercent = pct((*raw.DiskTotal - *raw.DiskFree) / *raw.DiskTotal * 100)
	}
	if raw.UptimeSecs != nil {
		m.Uptime = humanDuration(*raw.UptimeSecs)
	}
	for _, p := range raw.Processes {
		pi := model.ProcessInfo{Name: p.Name}
		if p.CPU != nil {
			pi.CPUPercent = *p.CPU
		}
		if p.Mem != nil {
			pi.MemPercent = *p.Mem
		}
		m.Processes = append(m.Processes, pi)
		if len(m.Processes) == 3 {
			break
		}
	}
	for _, g := range raw.GPUs {
		gi := model.GPUInfo{Name: g.Name}
		if g.Util != nil {
			gi.UtilPct = *g.Util
		}
		if g.Used != nil && g.Total != nil && *g.Total > 0 {
			gi.MemoryUsed = int64(*g.Used)
			gi.MemoryTot = int64(*g.Total)
			gi.MemoryPct = *g.Used / *g.Total * 100
		}
		m.GPUs = append(m.GPUs, gi)
	}
	return m, true
}

func pct(v float64) *float64 {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func humanDuration(seconds float64) string {
	d := int64(seconds)
	days := d / 86400
	hours := (d % 86400) / 3600
	minutes := (d % 3600) / 60
	switch {
	case days > 0:
		return strconv.FormatInt(days, 10) + " days"
	case hours > 0:
		return strconv.FormatInt(hours, 10) + "h " + strconv.FormatInt(minutes, 10) + "m"
	default:
		return strconv.FormatInt(minutes, 10) + "m"
	}
}
