package server

import (
	"bufio"
	"encoding/json"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mc-go/internal/world"
)

// server.properties, as the real 26.2 server keeps it: on startup it
// reads the file, fills every key it knows with its default, and writes
// the whole set back - keys sorted, Java Properties escaping, a two-line
// comment header (the second line the store time). The defaults below
// are the file a fresh 26.2 server writes into an empty directory, read
// back key for key (management-server-secret is a fresh random token).
var propertyDefaults = map[string]string{
	"accepts-transfers":                      "false",
	"allow-flight":                           "false",
	"broadcast-console-to-ops":               "true",
	"broadcast-rcon-to-ops":                  "true",
	"bug-report-link":                        "",
	"chat-spam-threshold-seconds":            "10",
	"command-spam-threshold-seconds":         "10",
	"difficulty":                             "easy",
	"enable-code-of-conduct":                 "false",
	"enable-jmx-monitoring":                  "false",
	"enable-query":                           "false",
	"enable-rcon":                            "false",
	"enable-status":                          "true",
	"enforce-secure-profile":                 "true",
	"enforce-whitelist":                      "false",
	"entity-broadcast-range-percentage":      "100",
	"force-gamemode":                         "false",
	"function-permission-level":              "2",
	"gamemode":                               "survival",
	"generate-structures":                    "true",
	"generator-settings":                     "{}",
	"hardcore":                               "false",
	"hide-online-players":                    "false",
	"initial-disabled-packs":                 "",
	"initial-enabled-packs":                  "vanilla",
	"level-name":                             "world",
	"level-seed":                             "",
	"level-type":                             "minecraft:normal",
	"log-ips":                                "true",
	"management-server-allowed-origins":      "",
	"management-server-enabled":              "false",
	"management-server-host":                 "localhost",
	"management-server-port":                 "0",
	"management-server-secret":               "",
	"management-server-tls-enabled":          "true",
	"management-server-tls-keystore":         "",
	"management-server-tls-keystore-password": "",
	"max-chained-neighbor-updates":           "1000000",
	"max-players":                            "20",
	"max-tick-time":                          "60000",
	"max-world-size":                         "29999984",
	"motd":                                   "A Minecraft Server",
	"network-compression-threshold":          "256",
	"online-mode":                            "true",
	"op-permission-level":                    "4",
	"pause-when-empty-seconds":               "60",
	"player-idle-timeout":                    "0",
	"prevent-proxy-connections":              "false",
	"query.port":                             "25565",
	"rate-limit":                             "0",
	"rcon.password":                          "",
	"rcon.port":                              "25575",
	"region-file-compression":                "deflate",
	"require-resource-pack":                  "false",
	"resource-pack":                          "",
	"resource-pack-id":                       "",
	"resource-pack-prompt":                   "",
	"resource-pack-sha1":                     "",
	"server-ip":                              "",
	"server-port":                            "25565",
	"simulation-distance":                    "10",
	"spawn-protection":                       "16",
	"status-heartbeat-interval":              "0",
	"sync-chunk-writes":                      "true",
	"text-filtering-config":                  "",
	"text-filtering-version":                 "0",
	"use-native-transport":                   "true",
	"view-distance":                          "10",
	"white-list":                             "false",
}

// ServerProperties is a loaded server.properties.
type ServerProperties struct {
	path   string
	values map[string]string
}

// LoadServerProperties reads path (a missing file is an empty one),
// fills the defaults and writes the complete file back, as the real
// server does on every start.
func LoadServerProperties(path string) (*ServerProperties, error) {
	p := &ServerProperties{path: path, values: map[string]string{}}
	if f, err := os.Open(path); err == nil {
		err = parseProperties(f, p.values)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for k, v := range propertyDefaults {
		if _, ok := p.values[k]; !ok {
			p.values[k] = v
		}
	}
	if p.values["management-server-secret"] == "" {
		p.values["management-server-secret"] = randomToken(40)
	}
	return p, p.store()
}

// Get returns a property's value.
func (p *ServerProperties) Get(key string) string { return p.values[key] }

func (p *ServerProperties) store() error {
	var b strings.Builder
	b.WriteString("#Minecraft server properties\n")
	b.WriteString("#" + time.Now().UTC().Format("Mon Jan 02 15:04:05 MST 2006") + "\n")
	keys := make([]string, 0, len(p.values))
	for k := range p.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(escapeProperty(k, true) + "=" + escapeProperty(p.values[k], false) + "\n")
	}
	if dir := filepath.Dir(p.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(p.path, []byte(b.String()), 0o644)
}

// escapeProperty is java.util.Properties.saveConvert as store(Writer)
// runs it (the real server writes UTF-8 text): backslash, the
// separators and comment characters escaped, a leading space (or any
// space in a key) escaped; other characters, non-ASCII included, as
// they are.
func escapeProperty(s string, key bool) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == ' ' && (key || i == 0):
			b.WriteString(`\ `)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '=' || r == ':' || r == '#' || r == '!':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func utf16Units(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)}
}

// parseProperties is java.util.Properties.load over UTF-8 text: '#'
// and '!' comment lines, backslash line continuations, the first
// unescaped '=', ':' or whitespace ending the key, and the usual
// escapes.
func parseProperties(f *os.File, out map[string]string) error {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<24)
	var logical strings.Builder
	cont := false
	for sc.Scan() {
		line := sc.Text()
		if cont {
			line = strings.TrimLeft(line, " \t\f")
		} else {
			t := strings.TrimLeft(line, " \t\f")
			if t == "" || t[0] == '#' || t[0] == '!' {
				continue
			}
			line = t
		}
		// An odd number of trailing backslashes continues the line.
		n := 0
		for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
			n++
		}
		if n%2 == 1 {
			logical.WriteString(line[:len(line)-1])
			cont = true
			continue
		}
		logical.WriteString(line)
		cont = false
		k, v := splitProperty(logical.String())
		out[k] = v
		logical.Reset()
	}
	if cont {
		k, v := splitProperty(logical.String())
		out[k] = v
	}
	return sc.Err()
}

func splitProperty(s string) (string, string) {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c == '=' || c == ':' || c == ' ' || c == '\t' || c == '\f' {
			break
		}
		i++
	}
	key := s[:min(i, len(s))]
	rest := ""
	if i < len(s) {
		rest = strings.TrimLeft(s[i:], " \t\f")
		if rest != "" && (rest[0] == '=' || rest[0] == ':') {
			rest = strings.TrimLeft(rest[1:], " \t\f")
		}
	}
	return unescapeProperty(key), unescapeProperty(rest)
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+5], 16, 16); err == nil {
					r := rune(v)
					// Join a surrogate pair.
					if r >= 0xD800 && r < 0xDC00 && i+10 < len(s) && s[i+5] == '\\' && s[i+6] == 'u' {
						if lo, err := strconv.ParseUint(s[i+7:i+11], 16, 16); err == nil && lo >= 0xDC00 && lo < 0xE000 {
							r = 0x10000 + (r-0xD800)<<10 + (rune(lo) - 0xDC00)
							i += 6
						}
					}
					if utf8.ValidRune(r) {
						b.WriteRune(r)
					}
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func randomToken(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, n)
	for i := range out {
		v, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		out[i] = alphabet[v.Int64()]
	}
	return string(out)
}

// ParseSeed is WorldOptions.parseSeed: blank is no seed (a new world
// draws a random one), a long parses as itself, anything else is its
// Java String.hashCode.
func ParseSeed(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, true
	}
	var h int32
	for _, u := range utf16Of(s) {
		h = 31*h + int32(u)
	}
	return int64(h), true
}

func utf16Of(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		out = append(out, utf16Units(r)...)
	}
	return out
}

// ApplyProperties configures s from server.properties: the keys mc-go
// models. serverDir is where the world directory (level-name) lives.
// skip names Server settings the command line set explicitly, which
// win over the file.
func (s *Server) ApplyProperties(p *ServerProperties, serverDir string, skip map[string]bool) error {
	set := func(key string, fn func(v string) error) error {
		if skip[key] {
			return nil
		}
		if err := fn(p.Get(key)); err != nil {
			return fmt.Errorf("server.properties %s: %w", key, err)
		}
		return nil
	}
	atoi := func(v string, def int) int {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
		return def
	}
	steps := []struct {
		key string
		fn  func(string) error
	}{
		{"server-port", func(v string) error {
			s.Addr = fmt.Sprintf("%s:%d", p.Get("server-ip"), atoi(v, 25565))
			return nil
		}},
		{"motd", func(v string) error { s.MOTD = v; return nil }},
		{"max-players", func(v string) error { s.MaxPlayers = atoi(v, 20); return nil }},
		{"spawn-protection", func(v string) error { s.SpawnProtection = atoi(v, 16); return nil }},
		{"online-mode", func(v string) error { s.OnlineMode = v == "true"; return nil }},
		{"view-distance", func(v string) error { s.ViewDistance = min(max(atoi(v, 10), 2), 32); return nil }},
		{"simulation-distance", func(v string) error { s.SimulationDistance = min(max(atoi(v, 10), 2), 32); return nil }},
		{"level-name", func(v string) error {
			s.LevelName = v
			s.WorldDir = filepath.Join(serverDir, v)
			s.WorldPath = ""
			return nil
		}},
		{"level-type", func(v string) error {
			switch strings.TrimPrefix(strings.ToLower(v), "minecraft:") {
			case "normal", "default":
				s.LevelType = LevelTypeNormal
			case "flat":
				s.LevelType = LevelTypeFlat
			case "amplified":
				s.LevelType = LevelTypeAmplified
			case "large_biomes":
				s.LevelType = LevelTypeLargeBiomes
			default:
				return fmt.Errorf("level type %q is not supported", v)
			}
			return nil
		}},
		{"level-seed", func(v string) error {
			if seed, ok := ParseSeed(v); ok {
				s.WorldSeed = seed
				return nil
			}
			// A new world draws a random seed; an existing one keeps
			// level.dat's.
			if _, err := os.Stat(filepath.Join(s.WorldDir, "level.dat")); err != nil {
				n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
				s.WorldSeed = n.Int64()
			}
			return nil
		}},
		{"generate-structures", func(v string) error { s.GenerateStructures = v != "false"; return nil }},
		{"generator-settings", func(v string) error {
			if s.LevelType != LevelTypeFlat {
				return nil
			}
			p, err := parseFlatSettings(v)
			if err != nil {
				return err
			}
			s.FlatPreset = p
			return nil
		}},
		{"gamemode", func(v string) error {
			switch strings.ToLower(v) {
			case "survival", "0":
				s.GameMode = GameModeSurvival
			case "creative", "1":
				s.GameMode = GameModeCreative
			case "adventure", "2":
				s.GameMode = GameModeAdventure
			case "spectator", "3":
				s.GameMode = GameModeSpectator
			default:
				return fmt.Errorf("unknown game mode %q", v)
			}
			return nil
		}},
		{"difficulty", func(v string) error {
			switch strings.ToLower(v) {
			case "peaceful", "0":
				s.difficulty = DifficultyPeaceful
			case "easy", "1":
				s.difficulty = DifficultyEasy
			case "normal", "2":
				s.difficulty = DifficultyNormal
			case "hard", "3":
				s.difficulty = DifficultyHard
			default:
				return fmt.Errorf("unknown difficulty %q", v)
			}
			return nil
		}},
		{"enable-rcon", func(v string) error {
			if v == "true" {
				s.RconAddr = fmt.Sprintf("%s:%d", p.Get("server-ip"), atoi(p.Get("rcon.port"), 25575))
				s.RconPassword = p.Get("rcon.password")
			}
			return nil
		}},
		{"enable-query", func(v string) error {
			if v == "true" {
				s.EnableQuery = true
				s.QueryAddr = fmt.Sprintf("%s:%d", p.Get("server-ip"), atoi(p.Get("query.port"), 25565))
			}
			return nil
		}},
	}
	for _, st := range steps {
		if err := set(st.key, st.fn); err != nil {
			return err
		}
	}
	return nil
}

// parseFlatSettings reads a flat world's generator-settings JSON: its
// layers (bottom up) and biome; blank or {} is the default preset.
func parseFlatSettings(v string) (*world.FlatPreset, error) {
	v = strings.TrimSpace(v)
	def := world.DefaultFlatPreset()
	if v == "" || v == "{}" {
		return def, nil
	}
	var doc struct {
		Layers []struct {
			Block  string `json:"block"`
			Height int    `json:"height"`
		} `json:"layers"`
		Biome string `json:"biome"`
	}
	if err := json.Unmarshal([]byte(v), &doc); err != nil {
		return nil, err
	}
	p := &world.FlatPreset{Biome: def.Biome, Layers: def.Layers}
	if doc.Biome != "" {
		p.Biome = doc.Biome
		if !strings.Contains(p.Biome, ":") {
			p.Biome = "minecraft:" + p.Biome
		}
	}
	if doc.Layers != nil {
		p.Layers = nil
		for _, l := range doc.Layers {
			b := l.Block
			if !strings.Contains(b, ":") {
				b = "minecraft:" + b
			}
			p.Layers = append(p.Layers, world.FlatLayer{Block: b, Height: l.Height})
		}
	}
	return p, nil
}
