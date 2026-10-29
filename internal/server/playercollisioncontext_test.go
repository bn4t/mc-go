package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestPlayerContextCollisionGates(t *testing.T) {
	scaffold := mkBlockState(t, "scaffolding", map[string]string{"distance": "0", "bottom": "false"})
	unstable := mkBlockState(t, "scaffolding", map[string]string{"distance": "1", "bottom": "true"})
	powder := mkBlockState(t, "powder_snow", nil)
	for _, tc := range []struct {
		name    string
		block   world.Block
		context playerShapeContext
		want    []cellBox
		stable  bool
	}{
		{"scaffold_inside", scaffold, playerShapeContext{feet: 0}, nil, false},
		{"scaffold_below_epsilon", scaffold, playerShapeContext{feet: .99998}, nil, false},
		{"scaffold_above", scaffold, playerShapeContext{feet: 1}, nil, true},
		{"scaffold_descending", scaffold, playerShapeContext{feet: 1, descending: true}, nil, false},
		{"scaffold_unstable_floor", unstable, playerShapeContext{feet: 0}, []cellBox{{0, 0, 0, 1, .125, 1}}, false},
		{"scaffold_unstable_descending_floor", unstable, playerShapeContext{feet: 1, descending: true}, []cellBox{{0, 0, 0, 1, .125, 1}}, false},
		{"scaffold_below_unstable_floor", unstable, playerShapeContext{feet: -.00002}, nil, false},
		{"scaffold_placement", unstable, playerShapeContext{feet: 1, placement: true}, nil, false},
		{"powder_barefoot", powder, playerShapeContext{feet: 1}, nil, false},
		{"powder_boots", powder, playerShapeContext{feet: 1, leatherBoots: true}, []cellBox{{0, 0, 0, 1, 1, 1}}, false},
		{"powder_below_boot_gate", powder, playerShapeContext{feet: .99998, leatherBoots: true}, nil, false},
		{"powder_boot_epsilon", powder, playerShapeContext{feet: .999995, leatherBoots: true}, []cellBox{{0, 0, 0, 1, 1, 1}}, false},
		{"powder_descending", powder, playerShapeContext{feet: 1, leatherBoots: true, descending: true}, nil, false},
		{"powder_fall_threshold", powder, playerShapeContext{feet: 1, fallDistance: 2.5}, nil, false},
		{"powder_fall_shape", powder, playerShapeContext{feet: .8, fallDistance: 2.5001, descending: true}, []cellBox{{0, 0, 0, 1, .8999999761581421, 1}}, false},
		{"powder_placement", powder, playerShapeContext{feet: 1, fallDistance: 3, leatherBoots: true, placement: true}, nil, false},
	} {
		got := playerContextShapeAt(tc.block, world.Pos{}, tc.context)
		if tc.stable {
			if len(got) != 7 {
				t.Errorf("%s stable boxes%v", tc.name, got)
			}
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s boxes%v want%v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s boxes%v want%v", tc.name, got, tc.want)
			}
		}
	}
}

func TestPlayerPosePowderEquipmentContext(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Powder", .5, .999995, .5)
	sess.world().SetBlock(world.Pos{}, mkBlockState(t, "powder_snow", nil))
	if !s.playerCanFit(sess, 3) {
		t.Fatal("barefoot powder fit")
	}
	sess.inv[slotArmorStart+3] = ItemStack{ID: world.ItemIDs["leather_boots"], Count: 1}
	if s.playerCanFit(sess, 3) {
		t.Fatal("boots should retain collision inside the above epsilon")
	}
	sess.sneaking = true
	if !s.playerCanFit(sess, 3) {
		t.Fatal("descending boots should pass powder")
	}
	sess.setPos(.5, .8, .5, 0, 0, false)
	sess.fallDistance = 2.6
	if s.playerCanFit(sess, 3) {
		t.Fatal("fall collision overrides descending boots")
	}
}
