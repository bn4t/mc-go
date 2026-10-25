package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestVillagerTradeParity exercises the merchant menu against both
// servers: right-clicking a villager with an Offers NBT table opens the
// merchant screen (menu type 19) and sends merchant_offers;
// select_trade fills the payment slots and reveals the result; taking
// the result consumes the cost, lands the sell stack, and fires
// villager_trade (minecraft:adventure/trade).
func TestVillagerTradeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TradeP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f        *advFlush
		menu     int32
		offers   int32
		buyID    int32
		buyCount int32
		sellID   int32
		sellCnt  int32
		// after select_trade: payment slot + result visible in the
		// container sync
		payFilled   bool
		resultShown bool
		emeraldGot  bool
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "TradeP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:wheat 8")
		sendCommand(t, pw, "tp @s 8.5 -60 10.5 180 0")
		sendCommand(t, pw, `summon minecraft:villager 8.5 -60 8.5 {NoAI:1b,Offers:{Recipes:[{buy:{id:"minecraft:wheat",count:2},sell:{id:"minecraft:emerald",count:3}}]}}`)

		wheat, _ := world.ItemID("wheat")
		emerald, _ := world.ItemID("emerald")

		r := result{f: f, menu: -1, offers: -1}
		var winID, stateID int32
		end := time.Now().Add(14 * time.Second)
		villFound := false
		clicked := false
		traded := false
		deadline := func(d time.Duration) { _ = c.SetReadDeadline(time.Now().Add(d)) }
		for time.Now().Before(end) && !traded {
			deadline(time.Until(end))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				id, e1 := rp.VarInt()
				_, e2 := rp.Read(16)
				typ, e3 := rp.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 140 && !villFound {
					villFound = true
					var ib protocol.Buffer
					ib.VarInt(id)
					ib.VarInt(0) // interact
					ib.VarInt(0) // main hand
					ib.Bool(false)
					_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
				}
			case server.IDPlayOpenScreen:
				rp := protocol.NewPacketReader(p.Data)
				w, e1 := rp.VarInt()
				m, e2 := rp.VarInt()
				if e1 == nil && e2 == nil {
					winID = w
					r.menu = m
				}
			case server.IDPlayMerchantOffers:
				rp := protocol.NewPacketReader(p.Data)
				if _, err := rp.VarInt(); err != nil { // container id
					break
				}
				n, err := rp.VarInt()
				if err != nil {
					break
				}
				r.offers = n
				if n > 0 {
					// buy ItemCost, predicate byte, sell stack
					bid, _ := rp.VarInt()
					bcnt, _ := rp.VarInt()
					r.buyID, r.buyCount = bid, bcnt
					_, _ = rp.Read(1)
					sc, _ := rp.VarInt()
					si, _ := rp.VarInt()
					_, _ = rp.VarInt() // patch map
					r.sellID, r.sellCnt = si, sc
				}
			case server.IDPlayContainerContent:
				rp := protocol.NewPacketReader(p.Data)
				wid, _ := rp.VarInt()
				st, _ := rp.VarInt()
				if wid == winID {
					stateID = st
					n, _ := rp.VarInt()
					for i := int32(0); i < n; i++ {
						cnt, _ := rp.VarInt()
						if cnt > 0 {
							iid, _ := rp.VarInt()
							_, _ = rp.VarInt() // patch
							if i < 2 && iid == wheat {
								r.payFilled = true
							}
							if i == 2 && iid == emerald {
								r.resultShown = true
							}
							if i >= 3 && iid == emerald {
								r.emeraldGot = true
							}
						}
					}
				}
			case server.IDPlayContainerSlot:
				rp := protocol.NewPacketReader(p.Data)
				wid, _ := rp.VarInt()
				st, _ := rp.VarInt()
				slot, _ := rp.Short()
				cnt, _ := rp.VarInt()
				if wid == winID {
					stateID = st
					var iid int32
					if cnt > 0 {
						iid, _ = rp.VarInt()
						_, _ = rp.VarInt()
					}
					if slot >= 0 && slot < 2 && iid == wheat {
						r.payFilled = true
					}
					if slot == 2 && iid == emerald {
						r.resultShown = true
					}
					if slot >= 3 && iid == emerald {
						r.emeraldGot = true
					}
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if villFound && r.menu >= 0 && r.offers > 0 && !r.payFilled {
				var sb protocol.Buffer
				sb.VarInt(0)
				_ = pw.WritePacket(server.IDPlaySelectTradeSB, sb.Bytes())
			}
			if r.resultShown && !clicked {
				clicked = true
				var cb protocol.Buffer
				cb.VarInt(winID)
				cb.VarInt(stateID)
				cb.Short(2)
				cb.Byte(0)
				cb.VarInt(0)
				cb.VarInt(0)   // no changed slots
				cb.Bool(false) // empty carried
				_ = pw.WritePacket(server.IDPlayClickContainerSB, cb.Bytes())
				// short tail: the take + trigger arrive over the next
				// reads; stay in this loop so slot updates aren't eaten
				// by a helper drain.
				end = time.Now().Add(1500 * time.Millisecond)
			}
			if clicked && f.prog["minecraft:adventure/trade"]["traded"] && r.emeraldGot {
				traded = true
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if want.menu != 19 {
		t.Fatalf("vanilla: merchant menu type = %d, want 19", want.menu)
	}
	if want.offers != 1 || want.buyID != world.ItemIDs["wheat"] || want.sellID != world.ItemIDs["emerald"] || want.buyCount != 2 || want.sellCnt != 3 {
		t.Fatalf("vanilla: offer parsed as %+v", want)
	}
	if !want.payFilled || !want.resultShown {
		t.Fatalf("vanilla: select_trade did not fill payment/result (pay=%v res=%v)", want.payFilled, want.resultShown)
	}
	if !want.f.prog["minecraft:adventure/trade"]["traded"] {
		t.Fatalf("vanilla: adventure/trade not granted after trade take")
	}
	if !want.emeraldGot {
		t.Fatalf("vanilla: emerald never landed in player menu slots")
	}

	diffAdvFlush(want.f, got.f)
	if got.menu != want.menu {
		t.Errorf("mc-go menu type = %d, vanilla %d", got.menu, want.menu)
	}
	if got.offers != want.offers || got.buyID != want.buyID || got.buyCount != want.buyCount || got.sellID != want.sellID || got.sellCnt != want.sellCnt {
		t.Errorf("mc-go offer = %+v, want %+v", got, want)
	}
	if got.payFilled != want.payFilled {
		t.Errorf("mc-go payment slot filled = %v, vanilla %v", got.payFilled, want.payFilled)
	}
	if got.resultShown != want.resultShown {
		t.Errorf("mc-go result shown = %v, vanilla %v", got.resultShown, want.resultShown)
	}
	if got.emeraldGot != want.emeraldGot {
		t.Errorf("mc-go emerald in inventory = %v, vanilla %v", got.emeraldGot, want.emeraldGot)
	}
}
