package server

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"mc-go/internal/protocol"
)

func TestJavaStatisticsUseCategorySpecificTargetRegistries(t *testing.T) {
	// Independent official registries.json observations: calcite BLOCK (not
	// state)1025->998, crafting_table ITEM360->333, zombie ENTITY151->150.
	common := []statKey{{statMined, 1025}, {statPickedUp, 360}, {statKilled, 151}}
	newer := []statKey{{statMined, 1012}, {statUsed, 40}, {statKilled, 130}} // cinnabar / sulfur_cube
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			sess := menuProjectionSession(t, s, number, "StatsCodec")
			all := append(append([]statKey(nil), common...), newer...)
			for i, key := range all {
				sess.addStat(key.category, key.id, int32(11+i))
			}
			before := append([]statKey(nil), sess.stats.insertOrder...)
			if err := s.sendStatistics(sess); err != nil {
				t.Fatal(err)
			}
			packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
			if packet.id != sess.ids().PlayAwardStats {
				t.Fatalf("stats packetID=%d", packet.id)
			}
			r := protocol.NewPacketReader(packet.data)
			n, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			expected := map[statKey]int32{{statMined, 1025}: 11, {statPickedUp, 360}: 12, {statKilled, 151}: 13, {statMined, 1012}: 14, {statUsed, 40}: 15, {statKilled, 130}: 16}
			if number == 775 {
				expected = map[statKey]int32{{statMined, 998}: 11, {statPickedUp, 333}: 12, {statKilled, 150}: 13}
			}
			if n != int32(len(expected)) {
				t.Fatalf("statistics count=%d want%d", n, len(expected))
			}
			for range n {
				category, e := r.VarInt()
				id, ie := r.VarInt()
				value, ve := r.VarInt()
				if e != nil || ie != nil || ve != nil {
					t.Fatalf("statistic decode=%v/%v/%v", e, ie, ve)
				}
				key := statKey{category, id}
				want, ok := expected[key]
				if !ok || value != want {
					t.Fatalf("unexpected wire statistic%+v=%d want%v", key, value, expected)
				}
				delete(expected, key)
			}
			if r.Remaining() != 0 || len(expected) != 0 {
				t.Fatalf("statistics tail=%d missing=%v", r.Remaining(), expected)
			}
			if !reflect.DeepEqual(before, sess.stats.insertOrder) {
				t.Fatal("viewer projection reordered canonical history")
			}
			for i, key := range all {
				if sess.stats.counts[key] != int32(11+i) {
					t.Fatalf("omitted statistic mutated canonical counter%+v", key)
				}
			}
			select {
			case <-sess.done:
				t.Fatal("known unrepresentable optional statistics disconnected viewer")
			default:
			}
		})
	}
}

func TestJavaStatisticsRejectUnknownIDsWithoutAffectingOtherViewer(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	bad := menuProjectionSession(t, s, 775, "BadStats")
	good := menuProjectionSession(t, s, 776, "GoodStats")
	key := statKey{statPickedUp, 1 << 29}
	bad.addStat(statCustom, CustomStatIDs["play_time"], 7)
	bad.addStat(key.category, key.id, 9)
	before := append([]statKey(nil), bad.stats.dirtyOrder...)
	if err := s.sendStatistics(bad); !errors.Is(err, ErrUnknownJavaStatistic) {
		t.Fatalf("unknown statistic error=%v", err)
	}
	if len(bad.sendCh) != 0 {
		t.Fatal("unknown statistic emitted a partial packet")
	}
	select {
	case <-bad.done:
	default:
		t.Fatal("corrupt statistics did not retire viewer")
	}
	select {
	case <-good.done:
		t.Fatal("corrupt statistics retired another viewer")
	default:
	}
	if !reflect.DeepEqual(before, bad.stats.dirtyOrder) || bad.stats.counts[key] != 9 {
		t.Fatal("failed projection changed canonical counters/history")
	}
	good.addStat(statPickedUp, 360, 3)
	if err := s.sendStatistics(good); err != nil {
		t.Fatal(err)
	}
	if len(good.sendCh) != 1 {
		t.Fatal("other viewer lost its valid statistics packet")
	}
	_ = materializeOutboundPacket(t, good, <-good.sendCh)
}
