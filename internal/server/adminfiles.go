package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The admin json files live in the server directory in vanilla's
// exact format — two-space indent, "forever" expiry, a
// "yyyy-MM-dd HH:mm:ss -0700" created stamp — so the four files are
// interchangeable with a real server's and diff cleanly in the
// blackbox suite.

type adminOpEntry struct {
	UUID                string `json:"uuid"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	BypassesPlayerLimit bool   `json:"bypassesPlayerLimit"`
}

type adminWLEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type adminBanEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created"`
	Source  string `json:"source"`
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

type adminIPBanEntry struct {
	IP      string `json:"ip"`
	Created string `json:"created"`
	Source  string `json:"source"`
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

// adminStamp renders created the way vanilla's ban entries do.
func adminStamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05 -0700")
}

// writeAdminFile writes one file under ServerDir, pretty-printed like
// vanilla (a missing ServerDir turns every write into a no-op, which
// keeps library embeddings and tests file-free).
func (s *Server) writeAdminFile(name string, v any) {
	if s.ServerDir == "" {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.ServerDir, name),
		bytes.TrimSuffix(buf.Bytes(), []byte("\n")), 0o644)
}

// LoadAdminFiles reads the four admin lists from ServerDir, creating
// each as "[]" when absent — vanilla creates them at boot.
func (s *Server) LoadAdminFiles() error {
	if s.ServerDir == "" {
		return nil
	}
	read := func(name string, out any) error {
		b, err := os.ReadFile(filepath.Join(s.ServerDir, name))
		if os.IsNotExist(err) {
			s.writeAdminFile(name, out)
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	var ops []adminOpEntry
	if err := read("ops.json", &ops); err != nil {
		return err
	}
	s.rulesMu.Lock()
	if s.ops == nil {
		s.ops = map[string]int{}
	}
	for _, e := range ops {
		s.ops[e.Name] = e.Level
	}
	s.opsList = ops
	s.rulesMu.Unlock()

	var wl []adminWLEntry
	if err := read("whitelist.json", &wl); err != nil {
		return err
	}
	var bans []adminBanEntry
	if err := read("banned-players.json", &bans); err != nil {
		return err
	}
	var ips []adminIPBanEntry
	if err := read("banned-ips.json", &ips); err != nil {
		return err
	}
	s.banMu.Lock()
	s.setWhitelistEntries(wl)
	if s.bannedNames == nil {
		s.bannedNames = map[string]string{}
	}
	for _, e := range bans {
		s.bannedNames[e.Name] = e.Reason
	}
	s.bansList = bans
	if s.bannedIPs == nil {
		s.bannedIPs = map[string]string{}
	}
	for _, e := range ips {
		s.bannedIPs[e.IP] = e.Reason
	}
	s.ipBansList = ips
	s.banMu.Unlock()
	return nil
}

// setWhitelistEntries installs the whitelist map and list together;
// banMu must be held.
func (s *Server) setWhitelistEntries(wl []adminWLEntry) {
	if s.whitelisted == nil {
		s.whitelisted = map[string]bool{}
	}
	for k := range s.whitelisted {
		delete(s.whitelisted, k)
	}
	for _, e := range wl {
		s.whitelisted[e.Name] = true
	}
	s.whitelistList = wl
}

// persistOpsLocked writes ops.json from opsList (rulesMu held).
func (s *Server) persistOpsLocked() {
	out := make([]adminOpEntry, len(s.opsList))
	copy(out, s.opsList)
	adminFilesOrder(out, func(e adminOpEntry) string { return e.UUID })
	s.writeAdminFile("ops.json", out)
}

// opListAdd records an op; opListDel removes it. rulesMu held.
func (s *Server) opListAdd(name string, level int) {
	for _, e := range s.opsList {
		if e.Name == name {
			return
		}
	}
	s.opsList = append(s.opsList, adminOpEntry{
		UUID: DashUUID(offlineUUID(name)), Name: name, Level: level,
	})
	s.persistOpsLocked()
}

func (s *Server) opListDel(name string) {
	keep := s.opsList[:0]
	for _, e := range s.opsList {
		if e.Name != name {
			keep = append(keep, e)
		}
	}
	s.opsList = append([]adminOpEntry(nil), keep...)
	s.persistOpsLocked()
}

// whitelistAdd/whitelistRemove keep list and map together (banMu held).
func (s *Server) whitelistAdd(name string) {
	for _, e := range s.whitelistList {
		if e.Name == name {
			return
		}
	}
	s.whitelistList = append(s.whitelistList, adminWLEntry{
		UUID: DashUUID(offlineUUID(name)), Name: name,
	})
	out := append([]adminWLEntry(nil), s.whitelistList...)
	adminFilesOrder(out, func(e adminWLEntry) string { return e.UUID })
	s.writeAdminFile("whitelist.json", out)
}

func (s *Server) whitelistRemove(name string) {
	keep := s.whitelistList[:0]
	for _, e := range s.whitelistList {
		if e.Name != name {
			keep = append(keep, e)
		}
	}
	s.whitelistList = append([]adminWLEntry(nil), keep...)
	out := append([]adminWLEntry(nil), s.whitelistList...)
	adminFilesOrder(out, func(e adminWLEntry) string { return e.UUID })
	s.writeAdminFile("whitelist.json", out)
}

// reloadWhitelist re-reads whitelist.json from ServerDir — vanilla's
// /whitelist reload replaces the list from disk.
func (s *Server) reloadWhitelist() {
	if s.ServerDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(s.ServerDir, "whitelist.json"))
	if err != nil {
		return
	}
	var wl []adminWLEntry
	if json.Unmarshal(b, &wl) == nil {
		s.banMu.Lock()
		s.setWhitelistEntries(wl)
		s.banMu.Unlock()
	}
}

// banListAdd/banListDel keep the banned-players list in vanilla order
// (banMu held); the name-keyed reason map is updated by the caller.
func (s *Server) banListAdd(name, reason, source string) {
	for _, e := range s.bansList {
		if e.Name == name {
			return
		}
	}
	s.bansList = append(s.bansList, adminBanEntry{
		UUID:    DashUUID(offlineUUID(name)),
		Name:    name,
		Created: adminStamp(time.Now()),
		Source:  source,
		Expires: "forever",
		Reason:  reason,
	})
	out := append([]adminBanEntry(nil), s.bansList...)
	adminFilesOrder(out, func(e adminBanEntry) string { return e.UUID })
	s.writeAdminFile("banned-players.json", out)
}

func (s *Server) banListDel(name string) {
	keep := s.bansList[:0]
	for _, e := range s.bansList {
		if e.Name != name {
			keep = append(keep, e)
		}
	}
	s.bansList = append([]adminBanEntry(nil), keep...)
	out := append([]adminBanEntry(nil), s.bansList...)
	adminFilesOrder(out, func(e adminBanEntry) string { return e.UUID })
	s.writeAdminFile("banned-players.json", out)
}

func (s *Server) ipBanListAdd(ip, reason, source string) {
	for _, e := range s.ipBansList {
		if e.IP == ip {
			return
		}
	}
	s.ipBansList = append(s.ipBansList, adminIPBanEntry{
		IP:      ip,
		Created: adminStamp(time.Now()),
		Source:  source,
		Expires: "forever",
		Reason:  reason,
	})
	out := append([]adminIPBanEntry(nil), s.ipBansList...)
	adminFilesOrder(out, func(e adminIPBanEntry) string { return e.IP })
	s.writeAdminFile("banned-ips.json", out)
}

func (s *Server) ipBanListDel(ip string) {
	keep := s.ipBansList[:0]
	for _, e := range s.ipBansList {
		if e.IP != ip {
			keep = append(keep, e)
		}
	}
	s.ipBansList = append([]adminIPBanEntry(nil), keep...)
	out := append([]adminIPBanEntry(nil), s.ipBansList...)
	adminFilesOrder(out, func(e adminIPBanEntry) string { return e.IP })
	s.writeAdminFile("banned-ips.json", out)
}

// onWhitelist reports whether name is whitelisted (banMu read inside).
func (s *Server) onWhitelist(name string) bool {
	s.banMu.Lock()
	defer s.banMu.Unlock()
	return s.whitelisted[name]
}

// adminFilesOrder sorts entries the way vanilla writes them: each
// StoredUserList iterates its HashMap<String, entry>, so file order
// is bucket order of the spread String hash of the key — the dashed
// uuid for player lists, the ip for banned-ips — with insertion
// order inside a bucket.
func adminFilesOrder[T any](items []T, key func(T) string) {
	if len(items) < 2 {
		return
	}
	c := hashMapCap(len(items))
	spread := func(h int32) int32 { return h ^ int32(uint32(h)>>16) }
	sort.SliceStable(items, func(i, j int) bool {
		return spread(javaStringHash(key(items[i])))&int32(c-1) <
			spread(javaStringHash(key(items[j])))&int32(c-1)
	})
}
