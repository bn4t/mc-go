package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mc-go/internal/world"
)

// The other two vanilla player stores: players/stats/<uuid>.json and
// players/advancements/<uuid>.json. The stats file groups counters by
// stat_type registry name; the advancements file records each
// criterion's obtain timestamp as "2006-01-02 15:04:05 -0700" and ends
// with DataVersion — both shapes measured on a 26.2 save.

// statFileName maps a statKey to the file's "type:value" pair, the
// same registry names statNameHash feeds the hash function.
func statFileName(k statKey) (cat, val string, ok bool) {
	if k.category < 0 || int(k.category) >= len(statTypeNames) {
		return "", "", false
	}
	var vname string
	switch k.category {
	case statMined:
		vname, ok = blockIDName(k.id)
	case statCrafted, statUsed, statBroken, statPickedUp, statDropped:
		vname, ok = world.ItemName(k.id)
	case statKilled, statKilledBy:
		vname, ok = EntityTypeName(k.id)
	case statCustom:
		vname, ok = customStatName(k.id)
	}
	if !ok {
		return "", "", false
	}
	return statTypeNames[k.category], vname, true
}

// statFileKey resolves a "category:name"/"name" pair back to a
// statKey.
func statFileKey(catName, valName string) (statKey, bool) {
	catName = strings.TrimPrefix(catName, "minecraft:")
	valName = strings.TrimPrefix(valName, "minecraft:")
	var cat int32
	for i, n := range statTypeNames {
		if n == catName {
			cat = int32(i)
		}
	}
	var id int32
	var ok bool
	switch cat {
	case statMined:
		id, ok = BlockID(valName)
	case statCrafted, statUsed, statBroken, statPickedUp, statDropped:
		id, ok = world.ItemID(valName)
	case statKilled, statKilledBy:
		id, ok = EntityTypeID(valName)
	case statCustom:
		id, ok = CustomStatID(valName)
	default:
		return statKey{}, false
	}
	return statKey{cat, id}, ok
}

// statsFileJSON renders the stats file in vanilla's layout. Entries
// follow StatsOrder, which preserves what a load read off the file —
// vanilla iterates its table when writing, so a reload replays the
// file's own sequence rather than an alphabetical one.
func statsFileJSON(p playerPersist) []byte {
	var b strings.Builder
	b.WriteString(`{"stats":{`)
	seen := map[string]bool{}
	cats := map[string][]string{}
	for _, key := range p.StatsOrder {
		k, ok := parseStatPersistKey(key)
		if !ok {
			continue
		}
		cat, val, ok := statFileName(k)
		if !ok {
			continue
		}
		cats[cat] = append(cats[cat], val)
		seen[key] = true
	}
	// Entries whose order was not recorded (hand-written data files)
	// append at the end of their category in sorted order.
	extra := map[string][]string{}
	for key := range p.Stats {
		if seen[key] {
			continue
		}
		k, ok := parseStatPersistKey(key)
		if !ok {
			continue
		}
		if cat, val, ok := statFileName(k); ok {
			extra[cat] = append(extra[cat], val)
		}
	}
	firstCat := true
	for cat := range cats {
		sort.Strings(extra[cat])
		cats[cat] = append(cats[cat], extra[cat]...)
	}
	// Category order is the order StatsOrder first mentioned them in;
	// categories only reachable through Stats' extras sort after.
	catOrder := []string{}
	wroteCat := map[string]bool{}
	for _, key := range p.StatsOrder {
		k, _ := parseStatPersistKey(key)
		cat, _, ok := statFileName(k)
		if ok && !wroteCat[cat] {
			wroteCat[cat] = true
			catOrder = append(catOrder, cat)
		}
	}
	rest := []string{}
	for cat := range cats {
		if !wroteCat[cat] {
			rest = append(rest, cat)
		}
	}
	sort.Strings(rest)
	catOrder = append(catOrder, rest...)
	for _, cat := range catOrder {
		if !firstCat {
			b.WriteByte(',')
		}
		firstCat = false
		fmt.Fprintf(&b, "%q:{", "minecraft:"+cat)
		for i, val := range cats[cat] {
			if i > 0 {
				b.WriteByte(',')
			}
			k, _ := statFileKey(cat, val)
			fmt.Fprintf(&b, "%q:%d", "minecraft:"+val, p.Stats[statPersistKey(k)])
		}
		b.WriteByte('}')
	}
	fmt.Fprintf(&b, `},"DataVersion":%d}`, world.DataVersion26_2)
	return []byte(b.String())
}

// advTimeFmt is the criterion timestamp a vanilla advancements file
// uses: local-time zone offset, space separated.
const advTimeFmt = "2006-01-02 15:04:05 -0700"

// advancementsFileJSON renders the advancements file. Advancement ids
// sort — the persisted record is a map, so no earlier order survives;
// vanilla's own order is hash-map iteration anyway.
func advancementsFileJSON(p playerPersist) []byte {
	var b strings.Builder
	b.WriteByte('{')
	ids := make([]string, 0, len(p.Advancements))
	for id := range p.Advancements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	first := true
	for _, id := range ids {
		if !first {
			b.WriteByte(',')
		}
		first = false
		name := id
		if !strings.Contains(name, ":") {
			name = "minecraft:" + name
		}
		a := p.Advancements[id]
		fmt.Fprintf(&b, "%q:{\"criteria\":{", name)
		crits := make([]string, 0, len(a.Criteria))
		for c := range a.Criteria {
			crits = append(crits, c)
		}
		sort.Strings(crits)
		for j, c := range crits {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%q:%q", c, time.UnixMilli(a.Criteria[c]).Format(advTimeFmt))
		}
		fmt.Fprintf(&b, `},"done":%v}`, a.Done)
	}
	fmt.Fprintf(&b, `,"DataVersion":%d}`, world.DataVersion26_2)
	return []byte(b.String())
}

// readStatsFile parses a stats/<uuid>.json into persist Stats and
// StatsOrder, keeping the file's own key sequence for StatsOrder.
func readStatsFile(raw []byte, p *playerPersist) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return
		}
		key, _ := keyTok.(string)
		if key != "stats" {
			var skip any
			_ = dec.Decode(&skip)
			continue
		}
		inner, err := dec.Token()
		if err != nil || inner != json.Delim('{') {
			return
		}
		for dec.More() {
			catTok, err := dec.Token()
			if err != nil {
				return
			}
			catName, _ := catTok.(string)
			vals, err := dec.Token()
			if err != nil || vals != json.Delim('{') {
				return
			}
			for dec.More() {
				statTok, err := dec.Token()
				if err != nil {
					return
				}
				statName, _ := statTok.(string)
				var n float64
				if err := dec.Decode(&n); err != nil {
					return
				}
				k, ok := statFileKey(catName, statName)
				if !ok {
					continue
				}
				pk := statPersistKey(k)
				if p.Stats == nil {
					p.Stats = map[string]int32{}
				}
				p.Stats[pk] = int32(n)
				p.StatsOrder = append(p.StatsOrder, pk)
			}
			_, _ = dec.Token() // '}' for the category
		}
		_, _ = dec.Token() // '}' for stats
	}
}

// readAdvancementsFile parses an advancements/<uuid>.json. Criterion
// timestamps become millis; an entry with no readable criteria but
// done:false is still progress vanilla would keep.
func readAdvancementsFile(raw []byte, p *playerPersist) {
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	for id, body := range f {
		if id == "DataVersion" {
			continue
		}
		var entry struct {
			Criteria map[string]string `json:"criteria"`
			Done     bool              `json:"done"`
		}
		if json.Unmarshal(body, &entry) != nil {
			continue
		}
		a := savedAdv{Done: entry.Done}
		for c, ts := range entry.Criteria {
			t, err := time.Parse(advTimeFmt, ts)
			if err != nil {
				continue
			}
			if a.Criteria == nil {
				a.Criteria = map[string]int64{}
			}
			a.Criteria[c] = t.UnixMilli()
		}
		if len(a.Criteria) == 0 && !a.Done {
			continue
		}
		if p.Advancements == nil {
			p.Advancements = map[string]savedAdv{}
		}
		p.Advancements[id] = a
	}
}

// savePlayerMetaFile writes the stats and advancements files for one
// persist — vanilla touches all three player files together on
// disconnect and on save-all.
func (s *Server) savePlayerMetaFile(p playerPersist) error {
	s.waitNativeWorldSaveOwned()
	return writeNativePlayerMetaFile(s.WorldDir, p)
}

func writeNativePlayerMetaFile(worldDir string, p playerPersist) error {
	if worldDir == "" || p.UUID == ([16]byte{}) {
		return nil
	}
	dash := DashUUID(p.UUID)
	dirs := []string{
		filepath.Join(worldDir, "players", "stats"),
		filepath.Join(worldDir, "players", "advancements"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dirs[0], dash+".json"), statsFileJSON(p), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dirs[1], dash+".json"), advancementsFileJSON(p), 0o644)
}

// savePlayerMetaFiles writes the stats and advancements files for
// every collected persist — the same set savePlayerFiles writes.
func (s *Server) savePlayerMetaFiles(persists map[[16]byte]playerPersist) {
	for u, p := range persists {
		if u == ([16]byte{}) {
			continue
		}
		if err := s.savePlayerMetaFile(p); err != nil && s.dataSaveErr == nil {
			s.dataSaveErr = err
		}
	}
}

// loadPlayerMetaFiles reads the stats and advancements companions of a
// uuid's .dat into the same saved-player entry.
func (s *Server) loadPlayerMetaFiles() {
	dir := filepath.Join(s.WorldDir, "players")
	s.savedPlayersMu.Lock()
	defer s.savedPlayersMu.Unlock()
	for key, p := range s.savedPlayers {
		u := p.UUID
		if u == ([16]byte{}) && strings.Count(key, "-") == 4 {
			// A uuid-keyed entry whose .dat carried no UUID field —
			// fall back to the filename key.
			continue
		}
		if u == ([16]byte{}) {
			continue
		}
		dash := DashUUID(u)
		if raw, err := os.ReadFile(filepath.Join(dir, "stats", dash+".json")); err == nil {
			readStatsFile(raw, &p)
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "advancements", dash+".json")); err == nil {
			readAdvancementsFile(raw, &p)
		}
		s.savedPlayers[key] = p
	}
}
