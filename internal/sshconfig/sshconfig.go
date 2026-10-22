// Package sshconfig parses ~/.ssh/config the way OpenSSH does: Host blocks may
// contain glob patterns, negations and several patterns, options are inherited
// with first-match-wins per option, and Include files are expanded in place.
//
// The parser never writes to the SSH config.
package sshconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Option is one directive inside a block.
type Option struct {
	Key   string
	Value string
}

// Block is a Host or Match section, or the implicit global section.
type Block struct {
	// Patterns are the Host patterns. Empty for the global section and for
	// Match blocks (which are always skipped for import).
	Patterns []string
	Options  []Option
	Global   bool
	Match    bool
	Line     int
	File     string
}

// Config is a parsed SSH configuration.
type Config struct {
	Blocks []Block
	Base   string
}

// Options is the resolved option set for one host alias.
type Options struct {
	Alias           string
	HostName        string
	User            string
	Port            int
	IdentityFiles   []string
	CertificateFile string
	IdentitiesOnly  bool
	ProxyJump       string
	LocalForwards   []string
	DynamicForward  string
	ForwardAgent    bool
	// set tracks first-match-wins for scalar options.
	set map[string]bool
}

// MaxIncludeDepth prevents Include cycles.
const MaxIncludeDepth = 8

// Parse parses SSH config text. base is the directory relative Include
// patterns resolve against (normally ~/.ssh).
func Parse(content, base string) (*Config, error) {
	c := &Config{Base: base}
	c.Blocks = append(c.Blocks, Block{Global: true, File: "<config>"})
	cur := 0
	visited := map[string]bool{}
	if err := c.parseInto(content, base, 0, visited, "<config>", &cur); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseFile reads and parses path.
func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(path)
	return Parse(string(data), base)
}

// LoadDefault parses ~/.ssh/config if it exists.
func LoadDefault() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "config")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Base: filepath.Join(home, ".ssh")}, nil
		}
		return nil, err
	}
	return ParseFile(path)
}

// parseInto appends blocks in the order ssh would read them. cur points at the
// block directives currently belong to, so an Include containing bare
// directives can extend the enclosing Host block as ssh_config(5) specifies.
func (c *Config) parseInto(content, base string, depth int, visited map[string]bool, file string, cur *int) error {
	if depth > MaxIncludeDepth {
		return nil
	}
	for _, cl := range logicalLines(content) {
		key, value, ok := splitDirective(cl.text)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "host":
			c.Blocks = append(c.Blocks, Block{Patterns: splitPatterns(value), Line: cl.line, File: file})
			*cur = len(c.Blocks) - 1
		case "match":
			// Match conditions are evaluated by ssh at connection time; for
			// import we cannot know them, so the block is skipped entirely.
			c.Blocks = append(c.Blocks, Block{Match: true, Line: cl.line, File: file})
			*cur = len(c.Blocks) - 1
		case "include":
			outer := *cur
			c.includeInto(value, base, depth, visited, cur)
			*cur = outer
		default:
			c.Blocks[*cur].Options = append(c.Blocks[*cur].Options, Option{Key: strings.ToLower(key), Value: value})
		}
	}
	return nil
}

func (c *Config) includeInto(value, base string, depth int, visited map[string]bool, cur *int) {
	for _, pattern := range shellFields(value) {
		pattern = expandHome(pattern)
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(base, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			abs, err := filepath.Abs(path)
			if err != nil {
				abs = path
			}
			if visited[abs] {
				continue
			}
			visited[abs] = true
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			_ = c.parseInto(string(data), filepath.Dir(path), depth+1, visited, path, cur)
		}
	}
}

type logicalLine struct {
	text string
	line int
}

// logicalLines joins backslash continuations and strips comments outside
// quotes, as ssh_config(5) specifies.
func logicalLines(content string) []logicalLine {
	raw := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var out []logicalLine
	var buf strings.Builder
	start := 0
	for i, line := range raw {
		trimmedRight := strings.TrimRight(line, " \t")
		continued := strings.HasSuffix(trimmedRight, "\\") && !insideComment(trimmedRight)
		if continued {
			if buf.Len() == 0 {
				start = i + 1
			}
			buf.WriteString(strings.TrimSuffix(trimmedRight, "\\"))
			buf.WriteByte(' ')
			continue
		}
		text := stripComment(line)
		if buf.Len() > 0 {
			buf.WriteString(text)
			text = buf.String()
			buf.Reset()
			out = append(out, logicalLine{text: strings.TrimSpace(text), line: start})
		} else if strings.TrimSpace(text) != "" {
			out = append(out, logicalLine{text: strings.TrimSpace(text), line: i + 1})
		}
	}
	if buf.Len() > 0 {
		out = append(out, logicalLine{text: strings.TrimSpace(buf.String()), line: start})
	}
	return out
}

func insideComment(s string) bool { return strings.Contains(s, "#") }

// stripComment removes an unquoted '#' comment.
func stripComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}

func splitDirective(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	idx := strings.IndexAny(line, " \t=")
	if idx < 0 {
		return line, "", true
	}
	key = line[:idx]
	value = strings.TrimSpace(line[idx:])
	value = strings.TrimPrefix(value, "=")
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return key, value, true
}

// splitPatterns splits a Host value into patterns, including negations.
func splitPatterns(value string) []string {
	return shellFields(value)
}

// shellFields splits on whitespace while honouring single and double quotes.
func shellFields(s string) []string {
	var out []string
	var buf strings.Builder
	var quote rune
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				buf.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return out
}

// Matches reports whether alias matches the block's patterns. A single
// negated pattern that matches excludes the host; otherwise at least one
// positive pattern must match.
func (b Block) Matches(alias string) bool {
	if b.Global {
		return true
	}
	if b.Match || len(b.Patterns) == 0 {
		return false
	}
	positive := false
	matchedPositive := false
	for _, p := range b.Patterns {
		negated := strings.HasPrefix(p, "!")
		if negated {
			p = p[1:]
			if matchPattern(p, alias) {
				return false
			}
			continue
		}
		positive = true
		if matchPattern(p, alias) {
			matchedPositive = true
		}
	}
	return !positive || matchedPositive
}

// matchPattern implements ssh's glob syntax: '*' and '?' only.
func matchPattern(pattern, name string) bool {
	pattern = strings.ToLower(pattern)
	name = strings.ToLower(name)
	// Iterative wildcard match to avoid exponential backtracking.
	pi, ni := 0, 0
	star, match := -1, 0
	for ni < len(name) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == name[ni]) {
			pi++
			ni++
			continue
		}
		if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			match = ni
			pi++
			continue
		}
		if star != -1 {
			pi = star + 1
			match++
			ni = match
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// Apply resolves all options for alias.
//
// Within a pass the first value wins, as in ssh(1). A block that names the
// alias literally is then applied as a second pass, so a specific block can
// override a wildcard default even when it appears later in the file. That is
// the behaviour asks for; strict ssh(1) would keep the earlier wildcard
// value instead, which is a common source of surprise.
func (c *Config) Apply(alias string) Options {
	wild := Options{Alias: alias, HostName: alias, Port: 22, set: map[string]bool{}}
	exact := Options{Alias: alias, HostName: alias, Port: 22, set: map[string]bool{}}
	for _, b := range c.Blocks {
		if !b.Matches(alias) {
			continue
		}
		if b.exact(alias) {
			exact.applyBlock(b)
		} else {
			wild.applyBlock(b)
		}
	}
	return mergeOptions(wild, exact)
}

// mergeOptions lets a literal Host block override wildcard-derived defaults.
func mergeOptions(wild, exact Options) Options {
	out := wild
	if exact.set["hostname"] {
		out.HostName = exact.HostName
	}
	if exact.set["user"] {
		out.User = exact.User
	}
	if exact.set["port"] {
		out.Port = exact.Port
	}
	if exact.set["certificatefile"] {
		out.CertificateFile = exact.CertificateFile
	}
	if exact.set["identitiesonly"] {
		out.IdentitiesOnly = exact.IdentitiesOnly
	}
	if exact.set["proxyjump"] {
		out.ProxyJump = exact.ProxyJump
	}
	if exact.set["dynamicforward"] {
		out.DynamicForward = exact.DynamicForward
	}
	if exact.set["forwardagent"] {
		out.ForwardAgent = exact.ForwardAgent
	}
	out.IdentityFiles = dedupe(append(append([]string{}, exact.IdentityFiles...), wild.IdentityFiles...))
	out.LocalForwards = dedupe(append(append([]string{}, exact.LocalForwards...), wild.LocalForwards...))
	return out
}

func (o *Options) applyBlock(b Block) {
	for _, opt := range b.Options {
		o.apply(opt)
	}
}

// exact reports whether a block names alias with a literal pattern.
func (b Block) exact(alias string) bool {
	for _, p := range b.Patterns {
		if strings.HasPrefix(p, "!") || strings.ContainsAny(p, "*?") {
			continue
		}
		if strings.EqualFold(p, alias) {
			return true
		}
	}
	return false
}

func (o *Options) apply(opt Option) {
	key := strings.ToLower(opt.Key)
	value := strings.TrimSpace(opt.Value)
	if value == "" {
		return
	}
	switch key {
	case "hostname":
		o.first("hostname", func() { o.HostName = expandTokens(opt.Value, o) })
	case "user":
		o.first("user", func() { o.User = opt.Value })
	case "port":
		o.first("port", func() {
			if p, err := strconv.Atoi(opt.Value); err == nil && p > 0 && p < 65536 {
				o.Port = p
			}
		})
	case "identityfile":
		// IdentityFile is additive, as in OpenSSH.
		for _, f := range shellFields(opt.Value) {
			o.IdentityFiles = append(o.IdentityFiles, expandHome(f))
		}
	case "certificatefile":
		o.first("certificatefile", func() {
			o.CertificateFile = expandHome(expandTokens(opt.Value, o))
		})
	case "identitiesonly":
		o.first("identitiesonly", func() { o.IdentitiesOnly = truthy(opt.Value) })
	case "proxyjump":
		o.first("proxyjump", func() { o.ProxyJump = opt.Value })
	case "localforward":
		for _, f := range splitForward(opt.Value) {
			o.LocalForwards = append(o.LocalForwards, f)
		}
	case "dynamicforward":
		o.first("dynamicforward", func() { o.DynamicForward = opt.Value })
	case "forwardagent":
		o.first("forwardagent", func() { o.ForwardAgent = truthy(opt.Value) })
	}
}

func (o *Options) first(key string, fn func()) {
	if o.set[key] {
		return
	}
	o.set[key] = true
	fn()
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "on", "1":
		return true
	}
	return false
}

// splitForward keeps a LocalForward value in one piece. OpenSSH permits a
// space separated form; normalise it to colon notation.
func splitForward(v string) []string {
	fields := shellFields(v)
	switch len(fields) {
	case 2:
		return []string{fields[0] + ":" + fields[1]}
	case 3:
		return []string{fields[0] + ":" + fields[1] + ":" + fields[2]}
	case 4:
		return []string{fields[0] + ":" + fields[1] + ":" + fields[2] + ":" + fields[3]}
	default:
		return fields
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// expandTokens expands the OpenSSH %-tokens useful in HostName and
// CertificateFile: %h (hostname), %n (original alias), %r (user), %p (port).
func expandTokens(v string, o *Options) string {
	r := strings.NewReplacer(
		"%%", "%",
		"%h", o.HostName,
		"%n", o.Alias,
		"%r", o.User,
		"%p", strconv.Itoa(o.Port),
	)
	return r.Replace(v)
}

// Aliases returns every concrete host alias defined in the configuration.
// Patterns containing globs are not aliases; they contribute defaults.
func (c *Config) Aliases() []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range c.Blocks {
		if b.Global || b.Match {
			continue
		}
		for _, p := range b.Patterns {
			p = strings.TrimPrefix(p, "!")
			if p == "" || strings.ContainsAny(p, "*?") {
				continue
			}
			key := strings.ToLower(p)
			if !seen[key] {
				seen[key] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// Hosts returns every concrete alias with its resolved options.
func (c *Config) Hosts() []Options {
	aliases := c.Aliases()
	out := make([]Options, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, c.Apply(a))
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// Debug renders the resolved options, useful in tests and bug reports.
func (o Options) Debug() string {
	return fmt.Sprintf("alias=%s host=%s user=%s port=%d identities=%v cert=%s jump=%s forwards=%v dynamic=%s agent=%v",
		o.Alias, o.HostName, o.User, o.Port, o.IdentityFiles, o.CertificateFile, o.ProxyJump, o.LocalForwards, o.DynamicForward, o.ForwardAgent)
}
